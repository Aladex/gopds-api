package testdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfiguredNeedsEveryRequiredVariable(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", map[string]string{}, false},
		{
			"host, user and name", map[string]string{
				"GOPDS_POSTGRES_DBHOST": "127.0.0.1:5432",
				"GOPDS_POSTGRES_DBUSER": "gopds",
				"GOPDS_POSTGRES_DBNAME": "gopds",
			}, true,
		},
		{
			// A password is optional: trust authentication and peer
			// authentication both leave it empty.
			"no password", map[string]string{
				"GOPDS_POSTGRES_DBHOST": "127.0.0.1:5432",
				"GOPDS_POSTGRES_DBUSER": "gopds",
				"GOPDS_POSTGRES_DBNAME": "gopds",
				"GOPDS_POSTGRES_DBPASS": "",
			}, true,
		},
		{
			// Half a request is still a request. Reading it as "no database
			// configured" is how one mistyped variable turns into a suite that
			// skips its integration tests and reports success.
			"host only", map[string]string{
				"GOPDS_POSTGRES_DBHOST": "127.0.0.1:5432",
			}, true,
		},
		{
			"name missing", map[string]string{
				"GOPDS_POSTGRES_DBHOST": "127.0.0.1:5432",
				"GOPDS_POSTGRES_DBUSER": "gopds",
			}, true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{
				"GOPDS_POSTGRES_DBHOST", "GOPDS_POSTGRES_DBUSER",
				"GOPDS_POSTGRES_DBNAME", "GOPDS_POSTGRES_DBPASS",
			} {
				t.Setenv(key, "")
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}

			cfg, ok := Configured()
			assert.Equal(t, tc.want, ok)
			if tc.want {
				assert.Equal(t, tc.env["GOPDS_POSTGRES_DBHOST"], cfg.Host)
				assert.Equal(t, tc.env["GOPDS_POSTGRES_DBNAME"], cfg.Name)
			}
		})
	}
}

// A half-configured database fails with the variable it is missing, not with
// a confusing connection error.
func TestConnectRejectsAHalfConfiguredDatabase(t *testing.T) {
	_, err := Connect(Config{Host: "127.0.0.1:5432"}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "GOPDS_POSTGRES_DBUSER")
	assert.Contains(t, err.Error(), "GOPDS_POSTGRES_DBNAME")
}

// A configured database that does not answer must be reported, not swallowed.
// This is the case the suites used to treat as "no database configured".
func TestConnectReportsAnUnreachableDatabase(t *testing.T) {
	// Port 1 is reserved and never listening.
	_, err := Connect(Config{Host: "127.0.0.1:1", User: "gopds", Name: "gopds"}, nil)

	require.Error(t, err, "an unreachable database must not look like success")
	assert.Contains(t, err.Error(), "127.0.0.1:1/gopds", "the message must name the target")
}
