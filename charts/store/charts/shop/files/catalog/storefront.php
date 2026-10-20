<?php
/**
 * Runs under `wp eval-file`: dresses the store from this directory, converging on every run.
 *
 *   - Brings the products in catalog.tsv into the store, with their categories, descriptions,
 *     prices, and images. Products are matched by slug, so reruns update them in place, and
 *     products the catalog does not list, such as ones created through chat, are left alone.
 *     An image is imported only when the product has none, so uploads do not pile up.
 *   - Applies the active theme's style variation, if one is named.
 *   - Publishes home.html as the front page, with its placeholders filled in.
 *   - Moves WordPress's sample page and post to the trash.
 *   - Sets the store's location and checkout rules, and marks WooCommerce's setup wizard
 *     done, since this script is what sets the store up.
 *
 * Usage: wp eval-file storefront.php <directory> [style variation]
 */

require_once ABSPATH . 'wp-admin/includes/file.php';
require_once ABSPATH . 'wp-admin/includes/media.php';
require_once ABSPATH . 'wp-admin/includes/image.php';

$dir = $args[0] ?? WP_CLI::error( 'usage: wp eval-file storefront.php <directory> [style variation]' );
$style = $args[1] ?? '';

/** Imports $file into the media library, attached to $parent, and returns its attachment id. */
function mirage_import_image( string $file, int $parent, string $title ): int {
	// media_handle_sideload moves its input into uploads, so hand it a copy.
	$tmp = wp_tempnam( basename( $file ) );
	copy( $file, $tmp );
	$id = media_handle_sideload( [ 'name' => basename( $file ), 'tmp_name' => $tmp ], $parent, $title );
	if ( is_wp_error( $id ) ) {
		WP_CLI::error( "importing $file: " . $id->get_error_message() );
	}
	return $id;
}

function mirage_category( string $name ): int {
	$term = get_term_by( 'name', $name, 'product_cat' );
	if ( $term ) {
		return $term->term_id;
	}
	$created = wp_insert_term( $name, 'product_cat' );
	if ( is_wp_error( $created ) ) {
		WP_CLI::error( "category $name: " . $created->get_error_message() );
	}
	return $created['term_id'];
}

function mirage_page_id( string $slug, string $type = 'page' ): int {
	$ids = get_posts( [ 'post_type' => $type, 'name' => $slug, 'post_status' => 'any', 'numberposts' => 1, 'fields' => 'ids' ] );
	return $ids[0] ?? 0;
}

// The catalog.
$images = [];
foreach ( file( "$dir/catalog.tsv", FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES ) as $line ) {
	if ( str_starts_with( $line, '#' ) ) {
		continue;
	}
	[ $slug, $name, $price, $category, , $description ] = explode( "\t", $line );
	$existing = mirage_page_id( $slug, 'product' );
	$product = $existing ? wc_get_product( $existing ) : new WC_Product_Simple();
	if ( ! $existing ) {
		$product->set_slug( $slug );
		$product->set_status( 'publish' );
	}
	$product->set_name( $name );
	$product->set_regular_price( $price );
	$product->set_description( $description );
	$product->set_category_ids( [ mirage_category( $category ) ] );
	$id = $product->save();
	if ( ! $product->get_image_id() ) {
		$product->set_image_id( mirage_import_image( "$dir/$slug.jpg", $id, $name ) );
		$product->save();
	}
	$images[ $slug ] = wp_get_attachment_image_url( $product->get_image_id(), 'large' );
	WP_CLI::log( sprintf( '%s %s (%s, %s)', $existing ? 'updated' : 'created', $name, $category, $price ) );
}

// The style variation: a block theme reads the site's look from its user global styles post.
if ( '' !== $style ) {
	$file = get_theme_file_path( "styles/$style.json" );
	$variation = is_readable( $file ) ? json_decode( file_get_contents( $file ), true ) : null;
	if ( ! $variation ) {
		WP_CLI::error( "the active theme has no style variation $style" );
	}
	$user_styles = WP_Theme_JSON_Resolver::get_user_data_from_wp_global_styles( wp_get_theme(), true );
	wp_update_post( wp_slash( [
		'ID'           => $user_styles['ID'],
		'post_content' => wp_json_encode( [
			'version'                     => $variation['version'] ?? 3,
			'isGlobalStylesUserThemeJSON' => true,
			'settings'                    => $variation['settings'] ?? new stdClass(),
			'styles'                      => array_merge( $variation['styles'] ?? [], [
				// The theme hyphenates and balances every heading, which splits the hero's
				// headline mid-word.
				'css' => '.wp-block-cover h1 { hyphens: manual; text-wrap: wrap; }',
			] ),
		] ),
	] ) );
	WP_CLI::log( "applied style variation $style" );
}

// The home page.
$hero = (int) get_option( 'mirage_hero_image_id' );
if ( ! $hero || ! wp_get_attachment_url( $hero ) ) {
	$hero = mirage_import_image( "$dir/hero.jpg", 0, 'Mirage Tea House' );
	update_option( 'mirage_hero_image_id', $hero );
}
$content = preg_replace_callback(
	'/\{\{(\w+)(?::([\w-]+))?\}\}/',
	function ( $m ) use ( $hero, $images ) {
		$url = match ( $m[1] ) {
			'hero' => wp_get_attachment_url( $hero ),
			'shop' => get_permalink( wc_get_page_id( 'shop' ) ),
			'cat'  => get_term_link( mirage_category( $m[2] ), 'product_cat' ),
			'img'  => $images[ $m[2] ] ?? WP_CLI::error( "home.html names unknown product image {$m[2]}" ),
		};
		return esc_url( $url );
	},
	file_get_contents( "$dir/home.html" )
);
$home = wp_insert_post( wp_slash( [
	'ID'           => mirage_page_id( 'home' ),
	'post_type'    => 'page',
	'post_name'    => 'home',
	'post_title'   => 'Home',
	'post_status'  => 'publish',
	'post_content' => $content,
	// Extendable's template without a page title; the cover block is the heading.
	'page_template' => 'no-title',
] ), true );
if ( is_wp_error( $home ) ) {
	WP_CLI::error( 'home page: ' . $home->get_error_message() );
}
update_option( 'show_on_front', 'page' );
update_option( 'page_on_front', $home );
WP_CLI::log( "published the home page ($home)" );

// WordPress's sample content would otherwise show in the header's page list.
foreach ( [ [ 'sample-page', 'page' ], [ 'hello-world', 'post' ] ] as [ $slug, $type ] ) {
	$id = mirage_page_id( $slug, $type );
	if ( $id && 'trash' !== get_post_status( $id ) ) {
		wp_trash_post( $id );
		WP_CLI::log( "trashed the sample $type $slug" );
	}
}

// Store settings. Customers need an account to check out, so they can sign in later and follow
// their orders under My account; they can create one at checkout or on My account. They choose
// their own password, because the generated one would arrive by email and this cluster sends none.
$settings = [
	'woocommerce_default_country'                       => 'IN:KA',
	'woocommerce_store_address'                         => '12 Church Street',
	'woocommerce_store_city'                            => 'Bengaluru',
	'woocommerce_store_postcode'                        => '560001',
	'timezone_string'                                   => 'Asia/Kolkata',
	'woocommerce_enable_guest_checkout'                 => 'no',
	'woocommerce_enable_checkout_login_reminder'        => 'yes',
	'woocommerce_enable_signup_and_login_from_checkout' => 'yes',
	'woocommerce_enable_myaccount_registration'         => 'yes',
	'woocommerce_registration_generate_username'        => 'yes',
	'woocommerce_registration_generate_password'        => 'no',
	// The setup wizard and its task list ask for what the settings above already hold.
	'woocommerce_onboarding_profile'                    => [ 'completed' => true ],
	'woocommerce_task_list_hidden_lists'                => [ 'setup', 'extended' ],
];
foreach ( $settings as $name => $value ) {
	update_option( $name, $value );
}
WP_CLI::log( 'applied ' . count( $settings ) . ' store settings' );
