package torn

import (
	"testing"
	"time"
)

func TestEnvironmentConfiguration(t *testing.T) {
	vars := map[string]string{"TORN_API_KEYS": "first, second", "TORN_API_KEY": "ignored", "TORN_API_BASE_URL_V2": "https://example.com/v2", "TORN_API_BASE_URL_V1": "https://example.com", "TORN_USER_AGENT": "sample/1.0", "TORN_HTTP_TIMEOUT_SECS": "12", "TORN_MAX_ATTEMPTS": "4", "TORN_NETWORK_RETRY_BACKOFF_MS": "123", "TORN_RATE_LIMIT_PER_KEY_PER_MIN": "95", "TORN_RATE_LIMIT_PER_IP_PER_MIN": "900", "TORN_MAX_IN_FLIGHT": "5"}
	for k, v := range vars {
		t.Setenv(k, v)
	}
	c, e := ConfigFromEnv()
	if e != nil {
		t.Fatal(e)
	}
	if len(c.APIKeys) != 2 || c.APIKeys[1] != "second" || c.Timeout != 12*time.Second || c.RetryDelay != 123*time.Millisecond || c.Attempts != 4 || c.KeyPerMinute != 95 || c.RoutePerMinute != 900 || c.MaxConcurrency != 5 || c.UserAgent != "sample/1.0" || c.BaseURL != "https://example.com/v2" || c.LegacyBaseURL != "https://example.com" {
		t.Fatalf("environment settings were not applied: %s", c)
	}
}
func TestInvalidEnvironmentLimits(t *testing.T) {
	for _, name := range []string{"TORN_HTTP_TIMEOUT_SECS", "TORN_NETWORK_RETRY_BACKOFF_MS", "TORN_MAX_ATTEMPTS", "TORN_MAX_IN_FLIGHT", "TORN_RATE_LIMIT_PER_KEY_PER_MIN", "TORN_RATE_LIMIT_PER_IP_PER_MIN"} {
		for _, value := range []string{"-1", "invalid", "99999999999999999999999999"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				t.Setenv(name, value)
				if _, e := ConfigFromEnv(); e == nil {
					t.Fatal("invalid setting accepted")
				}
			})
		}
	}
}

func TestDuplicateKeysShareOneQuotaIdentity(t *testing.T) {
	c, err := NewClient(Config{APIKeys: []string{" first ", "first", "second"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.keys) != 2 || len(c.cfg.APIKeys) != 2 || c.cfg.APIKeys[0] != "first" {
		t.Fatal("duplicate credentials created independent quota buckets")
	}
}
