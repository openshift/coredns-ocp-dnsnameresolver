package ocp_dnsnameresolver

import (
	"fmt"
	"strconv"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
)

const pluginName = "ocp_dnsnameresolver"

var log = clog.NewWithPlugin(pluginName)

func init() { plugin.Register(pluginName, setup) }

func setup(c *caddy.Controller) error {
	resolver, err := resolverParse(c)
	if err != nil {
		return plugin.Error(pluginName, err)
	}

	onStart, onShut, err := resolver.initPlugin()
	if err != nil {
		return plugin.Error(pluginName, err)
	}

	if onStart != nil {
		c.OnStartup(onStart)
	}
	if onShut != nil {
		c.OnShutdown(onShut)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		resolver.Next = next
		return resolver
	})

	return nil
}

func resolverParse(c *caddy.Controller) (*OCPDNSNameResolver, error) {
	resolver := New()

	i := 0
	for c.Next() {
		if i > 0 {
			return nil, plugin.ErrOnce
		}
		i++

		// There shouldn't be any more arguments.
		if len(c.RemainingArgs()) != 0 {
			return nil, c.ArgErr()
		}

		for c.NextBlock() {
			switch c.Val() {
			case "namespaces":
				args := c.RemainingArgs()
				if len(args) > 0 {
					for _, a := range args {
						resolver.namespaces[a] = struct{}{}
					}
					continue
				} else {
					return nil, c.ArgErr()
				}
			case "minTTL":
				args := c.RemainingArgs()
				if len(args) != 1 {
					return nil, c.ArgErr()
				}
				minTTL, err := strconv.Atoi(args[0])
				if err != nil {
					return nil, err
				}
				if minTTL <= 0 {
					return nil, fmt.Errorf("Value of minTTL should be greater than 0: %s", args[0])
				}
				resolver.minimumTTL = int32(minTTL)
			case "failureThreshold":
				args := c.RemainingArgs()
				if len(args) != 1 {
					return nil, c.ArgErr()
				}
				failureThreshold, err := strconv.Atoi(args[0])
				if err != nil {
					return nil, err
				}
				if failureThreshold <= 0 {
					return nil, fmt.Errorf("Value of failureThreshold should be greater than 0: %s", args[0])
				}
				resolver.failureThreshold = int32(failureThreshold)
			default:
				return nil, c.ArgErr()
			}
		}
	}
	return resolver, nil
}
