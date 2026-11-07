package companiondisplay

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	taterLinuxFirewall = "/usr/local/sbin/techo5-firewall"
	taterLinuxChain    = "TECHO5-IN"
)

type firewallCommand func(context.Context, string, ...string) ([]byte, error)

func ensureTaterLinuxFirewallPort(port int) {
	if _, err := os.Stat(taterLinuxFirewall); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errs := ensureFirewallPort(ctx, port, runFirewallCommand)
	for _, err := range errs {
		log.Printf("[companion-display] firewall: %v", err)
	}
}

func runFirewallCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ensureFirewallPort repairs the existing Tater Linux chain without creating
// or otherwise changing a host firewall. Both protocols are needed: HTTP uses
// TCP, while active discovery uses UDP. The rules are idempotent and inserted
// ahead of the chain's terminal DROP rule.
func ensureFirewallPort(ctx context.Context, port int, run firewallCommand) []error {
	portText := strconv.Itoa(port)
	var errs []error
	for _, tool := range []string{"iptables-legacy", "ip6tables-legacy"} {
		if output, err := run(ctx, tool, "-n", "-L", taterLinuxChain); err != nil {
			// A platform without this chain is not a Tater Linux firewall target.
			// Leave its network policy untouched.
			_ = output
			continue
		}
		for _, protocol := range []string{"tcp", "udp"} {
			rule := []string{"-p", protocol, "--dport", portText, "-j", "ACCEPT"}
			if _, err := run(ctx, tool, append([]string{"-C", taterLinuxChain}, rule...)...); err == nil {
				continue
			}
			output, err := run(ctx, tool, append([]string{"-I", taterLinuxChain, "1"}, rule...)...)
			if err != nil {
				detail := strings.TrimSpace(string(output))
				if detail != "" {
					errs = append(errs, fmt.Errorf("allow %s/%s with %s: %w: %s", portText, protocol, tool, err, detail))
				} else {
					errs = append(errs, fmt.Errorf("allow %s/%s with %s: %w", portText, protocol, tool, err))
				}
			}
		}
	}
	return errs
}
