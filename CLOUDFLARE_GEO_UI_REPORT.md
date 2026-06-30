# Cloudflare country-source and analytics UI correction

Date: 2026-06-27

Earlier builds could geolocate a Cloudflare edge address instead of the visitor, making many clicks appear in one country. This release accepts `CF-Connecting-IP` and `CF-IPCountry` only when Nginx confirms the actual network peer is inside Cloudflare's published proxy ranges. Direct clients cannot opt into or spoof that trust path.

Country-source priority:

1. Verified Cloudflare `CF-IPCountry`, stored immediately.
2. Cached or asynchronous IPinfo Lite fallback using the normalized visitor address.
3. Unknown when neither source is available.

The installer provides a root-owned baseline trust map and a weekly systemd updater. The updater downloads the official IPv4/IPv6 lists over HTTPS, validates every CIDR and list size, installs atomically, runs `nginx -t`, restores the previous file on failure, and reloads Nginx only after success.

A one-time `geo_country_source_version=2` migration clears only previous country codes and GeoIP cache rows. Click totals, rollups, unique-visitor hashes, browsers, devices, operating systems, referrers, timestamps, links, domains, and accounts are preserved. Old countries cannot be reconstructed because raw visitor IPs were never stored.

The peak-hours grid was replaced by one 24-hour chart with a shared baseline and scale, stable three-hour labels, exact tooltips, and peak highlighting. The world map now has responsive SVG rendering, logarithmic intensity, pointer/touch and keyboard interaction, selected-country styling, small-country markers, mobile country chips, and a non-overlapping legend.

After installation, enable Cloudflare **Network → IP Geolocation**, or enable the **Add visitor location headers** Managed Transform. IPinfo remains optional in **Settings → Country Analytics**.
