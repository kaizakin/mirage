# Upgrades and rollback

Mirage runs as two kinds of Helm release.

- The platform is one release, `mirage` in namespace `mirage`. It holds the gateway, the dashboard, the provisioner, currency-mcp, and Postgres.
- Every store is its own release, `store` in namespace `store-<slug>`. The provisioner installs it from the store chart baked into its image (`/charts/store`). It never upgrades a store after that.

Upgrade the platform first, then the stores. Each can be rolled back on its own.

The commands below use the VPS values file. On the local cluster, `make helm-upgrade` does the platform step.

## Before an upgrade

1. Build and push the new images under a new tag. Never reuse a tag: nodes keep pulled images by tag and do not fetch a newer image under an old one.

   ```sh
   make push-images REGISTRY=ghcr.io/kaizakin TAG=0.2.0
   ```

2. Set the new tag in `charts/platform/values-prod.yaml`. The tag appears once as the `&tag` anchor and once in `shop.setup.wpCliImage`. Commit the change, so the repository records what runs.

3. Back up Postgres. Rollback never undoes a migration (see [Migrations](#migrations)), so this dump is the way back from a bad schema change.

   ```sh
   kubectl -n mirage exec mirage-postgres-0 -- pg_dump -U mirage mirage > mirage-$(date +%F).sql
   ```

## Upgrade the platform

```sh
helm upgrade mirage charts/platform -n mirage -f charts/platform/values-prod.yaml --wait --timeout 10m
helm history mirage -n mirage
```

What each component does during the rollout:

- The gateway runs two replicas and rolls one at a time (`maxUnavailable: 0`), so `/mcp` stays up. MCP sessions held by a replica that stops end, and clients open new ones.
- The dashboard starts its new pod before it stops the old one.
- The provisioner uses `Recreate`: the old pod stops before the new one starts, because two provisioners would race on the same store releases. Store creation pauses for a few seconds. The provisioner keeps all of its state in Postgres, so it resumes where it stopped.
- Postgres is a StatefulSet. Its data volume stays.

The chart generates the admin password, the session key, and the secretbox key on first install and keeps them on every upgrade.

## Roll back the platform

```sh
helm history mirage -n mirage
helm rollback mirage <REVISION> -n mirage --wait --timeout 10m
```

A rollback restores that revision's manifests and values, including the image tags. It does not touch Postgres data or store releases.

## Migrations

The gateway embeds its migrations and applies them with goose when it starts. A Postgres advisory lock makes one replica apply them while the others wait. Migrations only run forward. Nothing runs a down migration, including `helm rollback`.

Every migration so far adds a table, a column, or a constraint. An older gateway therefore runs against a newer schema, and a rollback needs no database step. `internal/store/rollback_live_test.go` checks that an older set of migrations starts against a newer database.

An older gateway ignores tables it does not know. A rollback past a feature therefore switches the feature off until the next upgrade. For example, rolling back past `00007_tool_rules.sql` stops enforcing tool permissions.

Keep new migrations additive. A migration that drops or renames something breaks rollback: the older gateway would query what is gone. For such a change, ship it in two releases. The first release stops using the old column. The second release drops it. To undo a release that already applied a breaking migration, restore the dump from before the upgrade.

## Upgrade stores

A new provisioner installs new stores from its new chart. Stores that exist keep their old release until you upgrade them with `deploy/upgrade-stores.sh`.

Run the script from a checkout of the same revision as the deployed provisioner image, so the stores get the chart the provisioner would install.

```sh
deploy/upgrade-stores.sh --dry-run          # render every upgrade against the API server; change nothing
deploy/upgrade-stores.sh tea                # upgrade one store: release store in store-tea
deploy/upgrade-stores.sh                    # upgrade every store, one at a time
```

How the script behaves:

- It upgrades one store at a time and stops at the first failure.
- It runs `helm upgrade --reset-then-reuse-values`. The store gets the new chart's defaults, and keeps the values the provisioner installed it with: its host, title, and generated passwords.
- It reruns the store's setup Job as a post-upgrade hook and waits for it. Setup is idempotent: it keeps the WooCommerce application password and the catalog.
- With `--rollback-on-failure`, a store whose upgrade fails returns to its previous revision before the script stops.
- It skips stores that are not ready, such as one the provisioner is still creating. The provisioner reinstalls a creating store whose release is not `deployed`, so an upgrade must not race it.
- It prints the last five revisions of each store it upgraded.

Image tags and other values a store was installed with stay as they were. Pass new ones after `--`:

```sh
deploy/upgrade-stores.sh -- \
  --set woo-mcp.image.tag=0.2.0 \
  --set shop.setup.wpCliImage=ghcr.io/kaizakin/mirage-shop-setup:0.2.0
```

The same applies to a change in `provisioner.stores.values`, such as turning on TLS: new stores get it, and existing stores get it only through `--set` or `-f` here.

## Roll back a store

```sh
helm history store -n store-<slug>
helm rollback store <REVISION> -n store-<slug> --wait --timeout 15m
```

A store's volumes stay through a rollback, so its orders and products remain. If the rollback returns to the revision the provisioner installed, Helm recreates its setup Job, which runs once more and changes nothing.
