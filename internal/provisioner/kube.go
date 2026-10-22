package provisioner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/storage/driver"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// managedBy marks the namespaces the provisioner creates. It adopts and deletes no others, so
// a store slug can never name a namespace someone else owns.
const managedBy = "mirage-provisioner"

// Kube is the Cluster that Helm and the Kubernetes API provide. It finds the cluster the way
// kubectl does: in a pod, through the pod's service account.
type Kube struct {
	chartDir string
	client   kubernetes.Interface
}

// NewKube returns a Kube that installs the chart in chartDir.
func NewKube(chartDir string) (*Kube, error) {
	if _, err := loader.Load(chartDir); err != nil {
		return nil, fmt.Errorf("loading the store chart: %w", err)
	}
	config, err := cli.New().RESTClientGetter().ToRESTConfig()
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &Kube{chartDir: chartDir, client: client}, nil
}

func (k *Kube) helm(namespace string) (*action.Configuration, error) {
	settings := cli.New()
	settings.SetNamespace(namespace)
	cfg := action.NewConfiguration()
	if err := cfg.Init(settings.RESTClientGetter(), namespace, "secret"); err != nil {
		return nil, err
	}
	return cfg, nil
}

// EnsureNamespace implements Cluster.
func (k *Kube) EnsureNamespace(ctx context.Context, namespace, slug string) error {
	ns, err := k.client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": managedBy,
				"mirage/store":                 slug,
				// WordPress's image runs Apache as root, which the restricted level forbids.
				"pod-security.kubernetes.io/enforce": "baseline",
			},
		}}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if ns.Labels["app.kubernetes.io/managed-by"] != managedBy {
		return fmt.Errorf("namespace %s exists but was not created for a store", namespace)
	}
	if ns.DeletionTimestamp != nil {
		return fmt.Errorf("namespace %s is still being deleted", namespace)
	}
	return nil
}

// DeleteNamespace implements Cluster. Deleting the namespace deletes everything in it: the
// release, its volumes, and its Secrets.
func (k *Kube) DeleteNamespace(ctx context.Context, namespace string) (bool, error) {
	ns, err := k.client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if ns.Labels["app.kubernetes.io/managed-by"] != managedBy {
		return false, fmt.Errorf("refusing to delete namespace %s: it was not created for a store", namespace)
	}
	if ns.DeletionTimestamp == nil {
		err = k.client.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
	}
	return false, err
}

// Release implements Cluster.
func (k *Kube) Release(_ context.Context, namespace string) (ReleaseState, error) {
	cfg, err := k.helm(namespace)
	if err != nil {
		return 0, err
	}
	rel, err := action.NewStatus(cfg).Run(releaseName)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return ReleaseAbsent, nil
	}
	if err != nil {
		return 0, err
	}
	acc, err := release.NewAccessor(rel)
	if err != nil {
		return 0, err
	}
	switch acc.Status() {
	case "deployed":
		return ReleaseDeployed, nil
	case "uninstalled":
		return ReleaseAbsent, nil
	}
	// pending-install and the other pending states mean an operation stopped part way; failed
	// means Helm gave up. Either way it is uninstalled and installed afresh.
	return ReleaseStuck, nil
}

// Install implements Cluster.
func (k *Kube) Install(ctx context.Context, namespace string, values map[string]any) error {
	cfg, err := k.helm(namespace)
	if err != nil {
		return err
	}
	// Loaded per install: Helm edits a chart while resolving its dependencies, so stores
	// installing in parallel must not share one.
	chart, err := loader.Load(k.chartDir)
	if err != nil {
		return err
	}
	install := action.NewInstall(cfg)
	install.Namespace = namespace
	install.ReleaseName = releaseName
	// Return once the resources exist; Health watches them come up.
	install.WaitStrategy = kube.HookOnlyStrategy
	install.Timeout = time.Minute
	_, err = install.RunWithContext(ctx, chart, values)
	return err
}

// Uninstall implements Cluster.
func (k *Kube) Uninstall(_ context.Context, namespace string) error {
	cfg, err := k.helm(namespace)
	if err != nil {
		return err
	}
	uninstall := action.NewUninstall(cfg)
	uninstall.WaitStrategy = kube.HookOnlyStrategy
	uninstall.Timeout = time.Minute
	_, err = uninstall.Run(releaseName)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil
	}
	return err
}

// The resources Health watches, named after the release by the store chart.
const (
	setupJob     = releaseName + "-wordpress-setup"
	mcpServerApp = releaseName + "-woo-mcp"
)

// Health implements Cluster. A store is ready when its setup Job has finished and its MCP
// server is available.
func (k *Kube) Health(ctx context.Context, namespace string) (Health, error) {
	job, err := k.client.BatchV1().Jobs(namespace).Get(ctx, setupJob, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Health{Waiting: "the store's resources are being created"}, nil
	}
	if err != nil {
		return Health{}, err
	}
	setupDone := false
	for _, c := range job.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobFailed:
			return Health{Broken: fmt.Sprintf("store setup failed (%s): %s", c.Reason, c.Message)}, nil
		case batchv1.JobComplete:
			setupDone = true
		}
	}
	mcp, err := k.client.AppsV1().Deployments(namespace).Get(ctx, mcpServerApp, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return Health{}, err
	}
	if setupDone && err == nil && mcp.Status.AvailableReplicas > 0 {
		return Health{Ready: true}, nil
	}
	waiting, err := k.problem(ctx, namespace, job, setupDone)
	if err != nil {
		return Health{}, err
	}
	if waiting == "" {
		waiting = "the store's MCP server is not available yet"
	}
	return Health{Waiting: waiting}, nil
}

// problem names the first thing holding the store back: a pod that cannot be created (often
// the namespace's quota), then, until setup is done, where setup is, and after that a pod that
// cannot be scheduled or start its containers. Setup comes before the other pods because they
// wait on it: woo-mcp cannot start until setup publishes the store's credentials.
func (k *Kube) problem(ctx context.Context, namespace string, job *batchv1.Job, setupDone bool) (string, error) {
	events, err := k.client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: "type=Warning,reason=FailedCreate"})
	if err != nil {
		return "", err
	}
	if n := len(events.Items); n > 0 {
		newest := slices.MaxFunc(events.Items, func(a, b corev1.Event) int { return a.LastTimestamp.Compare(b.LastTimestamp.Time) })
		return fmt.Sprintf("%s %s: %s", strings.ToLower(newest.InvolvedObject.Kind), newest.InvolvedObject.Name, newest.Message), nil
	}
	if !setupDone {
		return k.setupProgress(ctx, namespace, job)
	}
	pods, err := k.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, pod := range pods.Items {
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
				return fmt.Sprintf("pod %s cannot be scheduled: %s", pod.Name, c.Message), nil
			}
		}
		for _, s := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
			if w := s.State.Waiting; w != nil && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
				return fmt.Sprintf("pod %s, container %s: %s %s", pod.Name, s.Name, w.Reason, w.Message), nil
			}
		}
	}
	return "", nil
}

// setupProgress says where the store's setup Job is: why its newest pod cannot run, or else
// the last line its setup script printed, which names the step it is on.
func (k *Kube) setupProgress(ctx context.Context, namespace string, job *batchv1.Job) (string, error) {
	pods, err := k.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/component=setup"})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "the store's setup has not started", nil
	}
	pod := slices.MaxFunc(pods.Items, func(a, b corev1.Pod) int { return a.CreationTimestamp.Compare(b.CreationTimestamp.Time) })
	setup := "store setup"
	if job.Status.Failed > 0 {
		setup = fmt.Sprintf("store setup (attempt %d)", job.Status.Failed+1)
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
			return fmt.Sprintf("%s cannot be scheduled: %s", setup, c.Message), nil
		}
	}
	for _, s := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if w := s.State.Waiting; w != nil && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
			return fmt.Sprintf("%s, container %s: %s %s", setup, s.Name, w.Reason, w.Message), nil
		}
	}
	tail := int64(1)
	raw, err := k.client.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "wp-setup", TailLines: &tail}).Do(ctx).Raw()
	if line := strings.TrimSpace(string(raw)); err == nil && line != "" {
		if len(line) > 200 {
			line = strings.ToValidUTF8(line[:200], "") + "…"
		}
		return fmt.Sprintf("%s is at: %s", setup, line), nil
	}
	return setup + " is still running", nil
}
