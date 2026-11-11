package config

import (
	"strings"
	"testing"
	"time"

	"gonginx-observer/internal/ipmask"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestDefaults(t *testing.T) {
	c, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Workers != 4 || c.WindowSeconds != 60 || c.PollInterval != 250*time.Millisecond ||
		c.MaskIPs != ipmask.Partial || c.Alert5xxRate != 0.2 || c.ReadFromStart {
		t.Errorf("padrões incorretos: %+v", c)
	}
}

func TestInvalidValuesAreReportedTogether(t *testing.T) {
	_, err := FromEnv(env(map[string]string{
		"OBSERVER_WORKERS":        "0",
		"OBSERVER_ALERT_5XX_RATE": "2",
		"OBSERVER_MASK_IPS":       "hash",
		"OBSERVER_POLL_INTERVAL":  "rápido",
	}))
	if err == nil {
		t.Fatal("esperava erro")
	}
	for _, name := range []string{"OBSERVER_WORKERS", "OBSERVER_ALERT_5XX_RATE", "OBSERVER_MASK_IPS", "OBSERVER_POLL_INTERVAL"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("erro não menciona %s: %v", name, err)
		}
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{"203.0.113.77": "203.0.113.0/24", "2001:db8:1:2::5": "2001:db8:1::/48", "(other)": "(other)"}
	for in, want := range cases {
		if got := ipmask.Partial.Apply(in); got != want {
			t.Errorf("Partial(%s) = %s, quer %s", in, got, want)
		}
		if got := ipmask.None.Apply(in); got != in {
			t.Errorf("None(%s) = %s", in, got)
		}
	}
}
