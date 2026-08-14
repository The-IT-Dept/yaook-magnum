// Package magnum renders magnum.conf from a MagnumDeployment plus the
// credentials resolved from the Yaook infra layer.
package magnum

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Section is a single INI section: option name -> value.
type Section map[string]string

// Config is an ordered-on-render INI document.
type Config map[string]Section

// CredentialInput carries everything the renderer needs that is not on the CR.
type CredentialInput struct {
	DBUser     string
	DBPassword string
	DBHost     string
	DBName     string

	MQUser     string
	MQPassword string
	MQHost     string

	KeystoneAuthURL    string
	KeystoneUsername   string
	KeystonePassword   string
	KeystoneProject    string
	KeystoneUserDomain string
	KeystoneProjDomain string

	RegionName string
	CAFile     string

	TrustDomainName    string
	TrustDomainID      string
	TrustAdminUser     string
	TrustAdminPassword string
}

// Render builds the magnum.conf content. Operator-generated values are written
// first, then overrides are merged on top, except for credential options which
// are re-applied afterwards so a bad override cannot break authentication.
func Render(in CredentialInput, overrides map[string]map[string]string) string {
	cfg := Config{}

	cfg["DEFAULT"] = Section{
		"transport_url": transportURL(in),
		"use_stderr":    "true",
		"debug":         "false",
		"host":          "magnum",
	}

	// magnum.conf's [api] bind option is "host" (an IPOpt). It defaults to
	// 127.0.0.1, which makes the container unreachable from the kubelet's
	// readiness probe and from the Service, so it must be set explicitly.
	cfg["api"] = Section{
		"host": "0.0.0.0",
		"port": "9511",
	}

	cfg["database"] = Section{
		"connection":              databaseURL(in),
		"connection_recycle_time": "280",
	}

	cfg["keystone_authtoken"] = Section{
		"auth_type":                    "password",
		"auth_version":                 "3",
		"auth_url":                     in.KeystoneAuthURL,
		"cafile":                       in.CAFile,
		"username":                     in.KeystoneUsername,
		"password":                     in.KeystonePassword,
		"project_name":                 in.KeystoneProject,
		"user_domain_name":             in.KeystoneUserDomain,
		"project_domain_name":          in.KeystoneProjDomain,
		"service_type":                 "container-infra",
		"interface":                    "internal",
		"valid_interfaces":             "internal",
		"service_token_roles":          "admin,service",
		"service_token_roles_required": "true",
	}

	// Magnum talks to the other OpenStack services using these credentials too.
	trust := Section{
		"trustee_keystone_interface": "internal",
		"roles":                      "member",
	}
	if in.TrustDomainName != "" {
		trust["trustee_domain_name"] = in.TrustDomainName
	}
	// Setting trustee_domain_id lets Magnum skip the domain-admin
	// authentication it would otherwise perform on every policy check.
	if in.TrustDomainID != "" {
		trust["trustee_domain_id"] = in.TrustDomainID
	}
	if in.TrustAdminUser != "" {
		trust["trustee_domain_admin_name"] = in.TrustAdminUser
	}
	if in.TrustAdminPassword != "" {
		trust["trustee_domain_admin_password"] = in.TrustAdminPassword
	}
	cfg["trust"] = trust

	// x509keypair keeps cluster certificates in Magnum's own database, which
	// avoids a hard dependency on Barbican.
	cfg["certificates"] = Section{
		"cert_manager_type": "x509keypair",
	}

	for _, client := range []string{
		"cinder_client", "barbican_client", "glance_client", "heat_client",
		"nova_client", "neutron_client", "octavia_client", "magnum_client",
	} {
		cfg[client] = Section{
			"region_name":   in.RegionName,
			"endpoint_type": "internalURL",
			"ca_file":       in.CAFile,
		}
	}

	cfg["oslo_messaging_notifications"] = Section{"driver": "noop"}
	cfg["conductor"] = Section{"workers": "1"}

	for section, opts := range overrides {
		if _, ok := cfg[section]; !ok {
			cfg[section] = Section{}
		}
		for k, v := range opts {
			cfg[section][k] = v
		}
	}

	// Re-assert credentials so user overrides cannot break auth.
	cfg["database"]["connection"] = databaseURL(in)
	cfg["DEFAULT"]["transport_url"] = transportURL(in)
	cfg["keystone_authtoken"]["username"] = in.KeystoneUsername
	cfg["keystone_authtoken"]["password"] = in.KeystonePassword
	cfg["keystone_authtoken"]["auth_url"] = in.KeystoneAuthURL
	cfg["api"]["host"] = "0.0.0.0"
	cfg["keystone_authtoken"]["www_authenticate_uri"] = in.KeystoneAuthURL
	if in.TrustDomainID != "" {
		cfg["trust"]["trustee_domain_id"] = in.TrustDomainID
	}

	return cfg.String()
}

// databaseURL mirrors the connection string format the upstream Yaook operators
// generate, including the TLS CA for the MariaDB frontend.
func databaseURL(in CredentialInput) string {
	return fmt.Sprintf("mysql+pymysql://%s:%s@%s:3306/%s?charset=utf8&ssl_ca=%s",
		in.DBUser, url.QueryEscape(in.DBPassword), in.DBHost, in.DBName, in.CAFile)
}

// transportURL points at the RabbitMQ frontend, which the infra-operator serves
// over TLS on 5671.
func transportURL(in CredentialInput) string {
	return fmt.Sprintf("rabbit://%s:%s@%s:5671//?ssl=1&ssl_ca_file=%s",
		in.MQUser, url.QueryEscape(in.MQPassword), in.MQHost, in.CAFile)
}

// String renders the config deterministically so that an unchanged spec does
// not produce a new Secret revision (and therefore no pointless rollout).
func (c Config) String() string {
	sections := make([]string, 0, len(c))
	for name := range c {
		sections = append(sections, name)
	}
	sort.Slice(sections, func(i, j int) bool {
		// DEFAULT first, then alphabetical, matching oslo.config convention.
		if sections[i] == "DEFAULT" {
			return true
		}
		if sections[j] == "DEFAULT" {
			return false
		}
		return sections[i] < sections[j]
	})

	var b strings.Builder
	for i, name := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s]\n", name)
		opts := make([]string, 0, len(c[name]))
		for k := range c[name] {
			opts = append(opts, k)
		}
		sort.Strings(opts)
		for _, k := range opts {
			fmt.Fprintf(&b, "%s = %s\n", k, c[name][k])
		}
	}
	return b.String()
}
