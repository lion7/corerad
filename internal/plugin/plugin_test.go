// Copyright 2020 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plugin

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/ndp"
)

func TestPluginString(t *testing.T) {
	tests := []struct {
		name string
		p    Plugin
		s    string
	}{
		{
			name: "DNSSL",
			p: &DNSSL{
				Lifetime:    30 * time.Second,
				DomainNames: []string{"foo.example.com", "bar.example.com"},
			},
			s: "domain names: [foo.example.com, bar.example.com], lifetime: 30s",
		},
		{
			name: "Prefix",
			p: &Prefix{
				Prefix:            mustCIDR("::/64"),
				OnLink:            true,
				Autonomous:        true,
				PreferredLifetime: 15 * time.Minute,
				ValidLifetime:     ndp.Infinity,
			},
			s: "::/64 [on-link, autonomous], preferred: 15m0s, valid: infinite",
		},
		{
			name: "MTU",
			p:    newMTU(1500),
			s:    "MTU: 1500",
		},
		{
			name: "RDNSS",
			p: &RDNSS{
				Lifetime: 30 * time.Second,
				Servers: []net.IP{
					mustIP("2001:db8::1"),
					mustIP("2001:db8::2"),
				},
			},
			s: "servers: [2001:db8::1, 2001:db8::2], lifetime: 30s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.s, tt.p.String()); diff != "" {
				t.Fatalf("unexpected string (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name    string
		plugins []Plugin
		ra      *ndp.RouterAdvertisement
	}{
		{
			name: "no config",
			ra:   &ndp.RouterAdvertisement{},
		},
		{
			name: "DNSSL",
			plugins: []Plugin{
				&DNSSL{
					Lifetime: 10 * time.Second,
					DomainNames: []string{
						"foo.example.com",
						"bar.example.com",
					},
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.DNSSearchList{
						Lifetime: 10 * time.Second,
						DomainNames: []string{
							"foo.example.com",
							"bar.example.com",
						},
					},
				},
			},
		},
		{
			name: "static prefix",
			plugins: []Plugin{
				&Prefix{
					Prefix:            mustCIDR("2001:db8::/32"),
					OnLink:            true,
					PreferredLifetime: 10 * time.Second,
					ValidLifetime:     20 * time.Second,
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength:      32,
						OnLink:            true,
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
						Prefix:            mustIP("2001:db8::"),
					},
				},
			},
		},
		{
			name: "automatic prefixes",
			plugins: []Plugin{
				&Prefix{
					Prefix:            mustCIDR("::/64"),
					OnLink:            true,
					Autonomous:        true,
					PreferredLifetime: 10 * time.Second,
					ValidLifetime:     20 * time.Second,

					Addrs: func() ([]net.Addr, error) {
						return []net.Addr{
							// Populate some addresses that should be ignored.
							mustCIDR("192.0.2.1/24"),
							&net.TCPAddr{},
							mustCIDR("fe80::1/64"),
							mustCIDR("fdff::1/32"),
							mustCIDR("2001:db8::1/64"),
							mustCIDR("2001:db8::2/64"),
							mustCIDR("fd00::1/64"),
						}, nil
					},
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength:                   64,
						OnLink:                         true,
						AutonomousAddressConfiguration: true,
						PreferredLifetime:              10 * time.Second,
						ValidLifetime:                  20 * time.Second,
						Prefix:                         mustIP("2001:db8::"),
					},
					&ndp.PrefixInformation{
						PrefixLength:                   64,
						OnLink:                         true,
						AutonomousAddressConfiguration: true,
						PreferredLifetime:              10 * time.Second,
						ValidLifetime:                  20 * time.Second,
						Prefix:                         mustIP("fd00::"),
					},
				},
			},
		},
		{
			name: "MTU",
			plugins: []Plugin{
				newMTU(1500),
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					ndp.NewMTU(1500),
				},
			},
		},
		{
			name: "RDNSS",
			plugins: []Plugin{
				&RDNSS{
					Lifetime: 10 * time.Second,
					Servers: []net.IP{
						mustIP("2001:db8::1"),
						mustIP("2001:db8::2"),
					},
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers: []net.IP{
							mustIP("2001:db8::1"),
							mustIP("2001:db8::2"),
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := new(ndp.RouterAdvertisement)

			var err error
			for _, p := range tt.plugins {
				got, err = p.Apply(got)
				if err != nil {
					t.Fatalf("failed to apply %q: %v", p.Name(), err)
				}
			}

			if diff := cmp.Diff(tt.ra, got); diff != "" {
				t.Fatalf("unexpected RA (-want +got):\n%s", diff)
			}

		})
	}
}

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panicf("failed to parse %q as IP address", s)
	}

	return ip
}

func mustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}

func newMTU(i int) *MTU {
	m := MTU(i)
	return &m
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}