<?php
// WordPress only offers application passwords over HTTPS; the in-cluster API runs over HTTP.
add_filter('wp_is_application_passwords_available', '__return_true');
