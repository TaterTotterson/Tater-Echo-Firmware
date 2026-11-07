package companiondisplay

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEnsureFirewallPortAddsMissingTCPAndUDPRules(t *testing.T) {
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		if strings.Contains(call, " -C ") {
			return nil, errors.New("missing")
		}
		return nil, nil
	}

	if errs := ensureFirewallPort(context.Background(), 8930, run); len(errs) != 0 {
		t.Fatalf("ensure firewall = %v", errs)
	}
	want := []string{
		"iptables-legacy -n -L TECHO5-IN",
		"iptables-legacy -C TECHO5-IN -p tcp --dport 8930 -j ACCEPT",
		"iptables-legacy -I TECHO5-IN 1 -p tcp --dport 8930 -j ACCEPT",
		"iptables-legacy -C TECHO5-IN -p udp --dport 8930 -j ACCEPT",
		"iptables-legacy -I TECHO5-IN 1 -p udp --dport 8930 -j ACCEPT",
		"ip6tables-legacy -n -L TECHO5-IN",
		"ip6tables-legacy -C TECHO5-IN -p tcp --dport 8930 -j ACCEPT",
		"ip6tables-legacy -I TECHO5-IN 1 -p tcp --dport 8930 -j ACCEPT",
		"ip6tables-legacy -C TECHO5-IN -p udp --dport 8930 -j ACCEPT",
		"ip6tables-legacy -I TECHO5-IN 1 -p udp --dport 8930 -j ACCEPT",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestEnsureFirewallPortKeepsExistingRulesAndSkipsForeignChains(t *testing.T) {
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		if name == "ip6tables-legacy" && strings.Contains(call, " -L ") {
			return nil, errors.New("chain missing")
		}
		return nil, nil
	}

	if errs := ensureFirewallPort(context.Background(), 8930, run); len(errs) != 0 {
		t.Fatalf("ensure firewall = %v", errs)
	}
	want := []string{
		"iptables-legacy -n -L TECHO5-IN",
		"iptables-legacy -C TECHO5-IN -p tcp --dport 8930 -j ACCEPT",
		"iptables-legacy -C TECHO5-IN -p udp --dport 8930 -j ACCEPT",
		"ip6tables-legacy -n -L TECHO5-IN",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestEnsureFirewallPortReportsInsertionFailures(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		if strings.HasPrefix(call, "-C ") {
			return nil, errors.New("missing")
		}
		if strings.HasPrefix(call, "-I ") {
			return []byte("permission denied\n"), errors.New("exit status 1")
		}
		return nil, nil
	}

	errs := ensureFirewallPort(context.Background(), 8930, run)
	if len(errs) != 4 {
		t.Fatalf("errors = %v", errs)
	}
	for _, err := range errs {
		if !strings.Contains(err.Error(), "8930") || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("error = %v", err)
		}
	}
}
