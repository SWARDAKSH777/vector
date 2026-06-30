package main

import (
	"net/http"
	"strings"
)

type clientClassification struct {
	Device  string
	Browser string
	OS      string
}

func classifyClient(r *http.Request) clientClassification {
	if r == nil {
		return clientClassification{Device: "Unknown", Browser: "Unknown", OS: "Unknown"}
	}
	ua := strings.ToLower(strings.TrimSpace(r.UserAgent()))
	mobileHint := strings.TrimSpace(r.Header.Get("Sec-CH-UA-Mobile"))
	platformHint := strings.ToLower(strings.Trim(r.Header.Get("Sec-CH-UA-Platform"), `" `))
	secCHUA := r.Header.Get("Sec-CH-UA")
	secCHUAFull := r.Header.Get("Sec-CH-UA-Full-Version-List")
	return clientClassification{
		Device:  parseDeviceWithHint(ua, mobileHint),
		Browser: parseBrowserWithHints(ua, secCHUA, secCHUAFull),
		OS:      parseOS(ua, platformHint),
	}
}

func parseDevice(ua string) string { return parseDeviceWithHint(strings.ToLower(ua), "") }

func parseDeviceWithHint(ua, mobileHint string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "ipad") || strings.Contains(l, "tablet") || strings.Contains(l, "kindle") || strings.Contains(l, "silk/"):
		return "Tablet"
	case mobileHint == "?1" || strings.Contains(l, "mobi") || strings.Contains(l, "iphone") || strings.Contains(l, "ipod") || strings.Contains(l, "android"):
		return "Mobile"
	case l == "":
		return "Unknown"
	default:
		return "Desktop"
	}
}

func parseBrowser(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case l == "":
		return "Unknown"
	case strings.Contains(l, "edg/") || strings.Contains(l, "edga/") || strings.Contains(l, "edgios/"):
		return "Edge"
	case strings.Contains(l, "opr/") || strings.Contains(l, "opera"):
		return "Opera"
	case strings.Contains(l, "samsungbrowser/"):
		return "Samsung Internet"
	case strings.Contains(l, "brave/") || strings.Contains(l, "bravebrowser"):
		// Brave iOS/Android explicitly includes "brave/" in the UA string.
		// Must come before Firefox and Chrome checks.
		return "Brave"
	case strings.Contains(l, "firefox/") || strings.Contains(l, "fxios/"):
		return "Firefox"
	case strings.Contains(l, "crios/") || strings.Contains(l, "chrome/") || strings.Contains(l, "chromium/"):
		return "Chrome"
	case strings.Contains(l, "msie ") || strings.Contains(l, "trident/"):
		return "Internet Explorer"
	case strings.Contains(l, "safari/") && !strings.Contains(l, "chrome/") && !strings.Contains(l, "crios/") && !strings.Contains(l, "android"):
		return "Safari"
	case strings.HasPrefix(l, "curl/"):
		return "curl"
	case strings.HasPrefix(l, "wget/"):
		return "wget"
	default:
		return "Other"
	}
}

// parseBrowserWithHints extends parseBrowser by also checking the
// Sec-CH-UA and Sec-CH-UA-Full-Version-List client-hint headers.
// Brave desktop sends a "Brave" brand token in these headers even though
// its UA string is otherwise identical to Chrome.
func parseBrowserWithHints(ua, secCHUA, secCHUAFullVersionList string) string {
	combined := strings.ToLower(secCHUA + " " + secCHUAFullVersionList)
	if strings.Contains(combined, "brave") {
		return "Brave"
	}
	return parseBrowser(ua)
}

func parseOS(ua, platformHint string) string {
	l := strings.ToLower(ua)
	p := strings.ToLower(platformHint)
	switch {
	case strings.Contains(p, "windows") || strings.Contains(l, "windows nt"):
		return "Windows"
	case strings.Contains(p, "android") || strings.Contains(l, "android"):
		return "Android"
	case strings.Contains(p, "ios") || strings.Contains(l, "iphone") || strings.Contains(l, "ipad") || strings.Contains(l, "ipod"):
		return "iOS"
	case strings.Contains(p, "mac") || strings.Contains(l, "macintosh") || strings.Contains(l, "mac os x"):
		return "macOS"
	case strings.Contains(p, "chrome os") || strings.Contains(l, "cros"):
		return "ChromeOS"
	case strings.Contains(p, "linux") || strings.Contains(l, "linux") || strings.Contains(l, "x11"):
		return "Linux"
	case l == "" && p == "":
		return "Unknown"
	default:
		return "Other"
	}
}

func clientIP(remoteAddr, xForwardedFor string) string {
	if xForwardedFor != "" {
		parts := strings.Split(xForwardedFor, ",")
		return strings.TrimSpace(parts[0])
	}
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		return remoteAddr[:idx]
	}
	return remoteAddr
}
