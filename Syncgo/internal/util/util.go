// Package util holds small helpers shared across packages.
package util

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net"
	"net/url"
	"strings"
)

func HumanBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / k
	i := 0
	for v >= k && i < len(units)-1 {
		v /= k
		i++
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}

func HTMLEscape(s string) string { return html.EscapeString(s) }

// IsPublicURL reports whether u points at a host the public internet can reach.
// Returns false for localhost, loopback, link-local, multicast, RFC1918 ranges.
// Unparseable hosts are treated as public.
func IsPublicURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}

// SecureHash derives an unguessable 10-char URL token from messageID using HMAC-SHA256.
// 10 base64url chars ≈ 60 bits of entropy → ~10^18 search space, safe against enumeration.
// Same input always produces same hash so URLs are stable.
func SecureHash(messageID int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d", messageID)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:10]
}
