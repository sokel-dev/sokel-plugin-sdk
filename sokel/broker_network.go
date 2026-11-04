// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokel

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/nats-io/nats.go"
	"github.com/sokel-dev/sokel-plugin-sdk/pluginenv"
)

// brokerNetworkError describes a configuration problem, not a transient outage. Enrollment and
// rediscovery must surface it instead of retrying forever or falling back to the public address.
type brokerNetworkError struct{ detail string }

func (e brokerNetworkError) Error() string { return e.detail }

func brokerNetwork() (string, error) {
	switch network := pluginenv.Get("BROKER_NETWORK"); network {
	case "", "public", "internal":
		return network, nil
	default:
		return "", brokerNetworkError{"SOKEL_BROKER_NETWORK must be public or internal, or unset"}
	}
}

func selectBrokerNetwork(a access, network string) (access, error) {
	if network != "internal" {
		return a, nil
	}
	if a.InternalURL == "" {
		return access{}, brokerNetworkError{"SOKEL_BROKER_NETWORK=internal requires nats.internal_url; configure the platform's internal broker address or upgrade the platform"}
	}
	address, err := internalBrokerAddress(a.InternalURL)
	if err != nil {
		return access{}, brokerNetworkError{"nats.internal_url: " + err.Error()}
	}
	a.URL = a.InternalURL
	a.internalDialAddress = address
	return a, nil
}

func selectOfflineBrokerNetwork(a access) (access, error) {
	switch a.Network {
	case "", "public":
		return a, nil
	case "internal":
		address, err := internalBrokerAddress(a.URL)
		if err != nil {
			return access{}, brokerNetworkError{"SOKEL_ACCESS.url: " + err.Error()}
		}
		a.internalDialAddress = address
		return a, nil
	default:
		return access{}, brokerNetworkError{"SOKEL_ACCESS.network must be public or internal, or omitted"}
	}
}

// Internal mode has exactly one destination. Embedded credentials, candidate lists and URL
// components NATS does not use are rejected so that the validated destination is also the dialed one.
func internalBrokerAddress(raw string) (string, error) {
	invalid := brokerNetworkError{"the internal broker URL must be one nats:// or tls:// URL with a host and an optional port (1-65535), without credentials, path, query, fragment, whitespace or comma-separated alternatives"}
	if strings.Contains(raw, ",") || strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return "", invalid
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "nats" && u.Scheme != "tls") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") {
		return "", invalid
	}
	// Brackets are only valid around an IP literal; an unbracketed IPv6 address is ambiguous.
	if strings.Contains(u.Hostname(), ":") && (!strings.HasPrefix(u.Host, "[") || net.ParseIP(u.Hostname()) == nil) ||
		strings.HasPrefix(u.Host, "[") && !strings.Contains(u.Hostname(), ":") {
		return "", invalid
	}
	port := u.Port()
	if port == "" {
		port = "4222"
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", invalid
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

// nats.go v1.48 adds INFO connect_urls to its server pool. Pinning actual dials prevents a later
// reconnect from reaching an advertised public endpoint without raising the SDK's Go requirement.
// SkipHostLookup leaves DNS resolution to net.Dialer, after the hostname has passed this check.
func brokerNetworkOptions(a access) []nats.Option {
	if a.internalDialAddress == "" {
		return nil
	}
	return []nats.Option{
		nats.SkipHostLookup(),
		nats.SetCustomDialer(&internalBrokerDialer{address: a.internalDialAddress, dialer: net.Dialer{Timeout: nats.DefaultTimeout}}),
	}
}

type internalBrokerDialer struct {
	address string
	dialer  net.Dialer
}

func (d *internalBrokerDialer) Dial(network, address string) (net.Conn, error) {
	if !strings.EqualFold(address, d.address) {
		return nil, fmt.Errorf("SOKEL_BROKER_NETWORK=internal refused a broker address outside nats.internal_url")
	}
	return d.dialer.Dial(network, address)
}
