package magnum

import (
	"strings"
	"testing"
)

func testInput() CredentialInput {
	return CredentialInput{
		DBUser: "api", DBPassword: "dbpw", DBHost: "magnum-magnum-db-frontend.yaook", DBName: "magnum",
		MQUser: "api", MQPassword: "mqpw", MQHost: "magnum-magnum-mq-frontend.yaook",
		KeystoneAuthURL:    "https://keystone.yaook.svc:5000/v3",
		KeystoneUsername:   "magnum-abc.yaook.cluster.local",
		KeystonePassword:   "kspw",
		KeystoneProject:    "service",
		KeystoneUserDomain: "Default",
		KeystoneProjDomain: "Default",
		RegionName:         "YaookRegion",
		CAFile:             "/etc/magnum/ca/ca-bundle.crt",
	}
}

func TestRenderContainsCoreSections(t *testing.T) {
	out := Render(testInput(), nil)
	for _, want := range []string{
		"[DEFAULT]", "[api]", "[database]", "[keystone_authtoken]",
		"[certificates]", "[trust]", "[conductor]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered config missing section %s", want)
		}
	}
}

func TestRenderDefaultSectionIsFirst(t *testing.T) {
	out := Render(testInput(), nil)
	if !strings.HasPrefix(out, "[DEFAULT]\n") {
		t.Errorf("expected [DEFAULT] first, got:\n%s", out[:min(80, len(out))])
	}
}

func TestDatabaseURLUsesTLSCA(t *testing.T) {
	out := Render(testInput(), nil)
	want := "mysql+pymysql://api:dbpw@magnum-magnum-db-frontend.yaook:3306/magnum?charset=utf8&ssl_ca=/etc/magnum/ca/ca-bundle.crt"
	if !strings.Contains(out, want) {
		t.Errorf("database connection string not as expected.\nwant substring: %s\ngot:\n%s", want, out)
	}
}

func TestTransportURLEnablesTLS(t *testing.T) {
	out := Render(testInput(), nil)
	want := "rabbit://api:mqpw@magnum-magnum-mq-frontend.yaook:5671//?ssl=1&ssl_ca_file=/etc/magnum/ca/ca-bundle.crt"
	if !strings.Contains(out, want) {
		t.Errorf("transport_url not as expected.\nwant substring: %s\ngot:\n%s", want, out)
	}
}

// Passwords containing URL-reserved characters must not corrupt the connection
// string.
func TestPasswordsAreURLEscaped(t *testing.T) {
	in := testInput()
	in.DBPassword = "p@ss/w:rd?&"
	out := Render(in, nil)
	if strings.Contains(out, "p@ss/w:rd?&") {
		t.Error("raw password with reserved characters leaked into connection URL unescaped")
	}
	if !strings.Contains(out, "p%40ss%2Fw%3Ard%3F%26") {
		t.Errorf("expected percent-encoded password in connection URL, got:\n%s", out)
	}
}

func TestOverridesAreApplied(t *testing.T) {
	out := Render(testInput(), map[string]map[string]string{
		"DEFAULT": {"debug": "true"},
		"newsect": {"foo": "bar"},
	})
	if !strings.Contains(out, "debug = true") {
		t.Error("override of an existing option was not applied")
	}
	if !strings.Contains(out, "[newsect]") || !strings.Contains(out, "foo = bar") {
		t.Error("override introducing a new section was not applied")
	}
}

// A user override must never be able to break authentication or connectivity.
func TestCredentialOverridesAreIgnored(t *testing.T) {
	out := Render(testInput(), map[string]map[string]string{
		"database":           {"connection": "mysql://evil/"},
		"keystone_authtoken": {"password": "wrong", "username": "wrong"},
	})
	if strings.Contains(out, "mysql://evil/") {
		t.Error("override was able to replace the database connection string")
	}
	if strings.Contains(out, "password = wrong") || strings.Contains(out, "username = wrong") {
		t.Error("override was able to replace keystone credentials")
	}
}

// Rendering must be deterministic, otherwise every reconcile would produce a
// new config hash and roll the pods forever.
func TestRenderIsDeterministic(t *testing.T) {
	in := testInput()
	overrides := map[string]map[string]string{"DEFAULT": {"a": "1", "b": "2", "c": "3"}}
	first := Render(in, overrides)
	for i := 0; i < 50; i++ {
		if got := Render(in, overrides); got != first {
			t.Fatalf("render is not deterministic on iteration %d", i)
		}
	}
}

// magnum-api defaults to binding 127.0.0.1, which is unreachable from the
// kubelet probe and the Service. Regression test for that.
func TestAPIBindsAllInterfaces(t *testing.T) {
	out := Render(testInput(), nil)
	if !strings.Contains(out, "host = 0.0.0.0") {
		t.Errorf("expected [api] host = 0.0.0.0, got:\n%s", out)
	}
	if strings.Contains(out, "host_ip") {
		t.Error("host_ip is not a valid magnum option; the bind option is [api] host")
	}
}

func TestAPIBindCannotBeOverridden(t *testing.T) {
	out := Render(testInput(), map[string]map[string]string{"api": {"host": "127.0.0.1"}})
	if strings.Contains(out, "host = 127.0.0.1") {
		t.Error("override was able to bind the API to loopback, which breaks readiness probes")
	}
}

func TestCertManagerAvoidsBarbican(t *testing.T) {
	out := Render(testInput(), nil)
	if !strings.Contains(out, "cert_manager_type = x509keypair") {
		t.Error("expected x509keypair so Magnum does not require Barbican")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
