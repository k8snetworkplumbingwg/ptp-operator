package tls

import (
	"context"
	"crypto/tls"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("tls")

// opensslToGoCipher maps OpenSSL cipher names (used in OpenShift TLS profiles)
// to Go crypto/tls cipher suite IDs. DHE-* ciphers have no Go equivalent and
// are silently skipped. TLS 1.3 ciphers are managed by the Go runtime and are
// also skipped.
var opensslToGoCipher = map[string]uint16{
	"ECDHE-ECDSA-AES128-GCM-SHA256":  tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	"ECDHE-RSA-AES128-GCM-SHA256":    tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	"ECDHE-ECDSA-AES256-GCM-SHA384":  tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	"ECDHE-RSA-AES256-GCM-SHA384":    tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	"ECDHE-ECDSA-CHACHA20-POLY1305":  tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	"ECDHE-RSA-CHACHA20-POLY1305":    tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	"ECDHE-ECDSA-AES128-SHA256":      tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
	"ECDHE-RSA-AES128-SHA256":        tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
	"ECDHE-ECDSA-AES128-SHA":         tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	"ECDHE-RSA-AES128-SHA":           tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	"ECDHE-ECDSA-AES256-SHA":         tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	"ECDHE-RSA-AES256-SHA":           tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	"AES128-GCM-SHA256":              tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	"AES256-GCM-SHA384":              tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	"AES128-SHA256":                  tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
	"AES128-SHA":                     tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	"AES256-SHA":                     tls.TLS_RSA_WITH_AES_256_CBC_SHA,
	"DES-CBC3-SHA":                   tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
}

// tls13Ciphers are TLS 1.3 cipher names in OpenSSL format. Go manages TLS 1.3
// cipher selection automatically and these cannot be set via CipherSuites.
var tls13Ciphers = map[string]bool{
	"TLS_AES_128_GCM_SHA256":       true,
	"TLS_AES_256_GCM_SHA384":       true,
	"TLS_CHACHA20_POLY1305_SHA256": true,
}

// protocolVersionMap maps OpenShift TLSProtocolVersion strings to Go constants.
var protocolVersionMap = map[configv1.TLSProtocolVersion]uint16{
	configv1.VersionTLS10: tls.VersionTLS10,
	configv1.VersionTLS11: tls.VersionTLS11,
	configv1.VersionTLS12: tls.VersionTLS12,
	configv1.VersionTLS13: tls.VersionTLS13,
}

// GetAPIServerTLSProfile fetches the TLS security profile from the
// APIServer.config.openshift.io/cluster resource.
func GetAPIServerTLSProfile(cfg *rest.Config) (*configv1.TLSSecurityProfile, error) {
	client, err := configv1client.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating openshift config client: %w", err)
	}

	apiServer, err := client.APIServers().Get(
		context.Background(), "cluster", metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("fetching APIServer config: %w", err)
	}

	return apiServer.Spec.TLSSecurityProfile, nil
}

// ResolveTLSProfileSpec resolves a TLSSecurityProfile to its concrete
// TLSProfileSpec containing ciphers and minimum TLS version. When profile is
// nil, the Intermediate profile is used as the default, matching OpenShift
// platform behaviour.
func ResolveTLSProfileSpec(profile *configv1.TLSSecurityProfile) *configv1.TLSProfileSpec {
	if profile == nil {
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}

	switch profile.Type {
	case configv1.TLSProfileCustomType:
		if profile.Custom != nil {
			return &profile.Custom.TLSProfileSpec
		}
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	case configv1.TLSProfileOldType:
		return configv1.TLSProfiles[configv1.TLSProfileOldType]
	case configv1.TLSProfileModernType:
		return configv1.TLSProfiles[configv1.TLSProfileModernType]
	default:
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}
}

// CipherSuites converts a list of OpenSSL cipher names to Go crypto/tls cipher
// suite IDs. Ciphers unsupported by Go (DHE-RSA-*, TLS 1.3 ciphers) are
// silently skipped since they are either handled by the Go runtime or have no
// Go equivalent.
func CipherSuites(opensslCiphers []string) []uint16 {
	var suites []uint16
	for _, name := range opensslCiphers {
		if tls13Ciphers[name] {
			continue
		}
		if id, ok := opensslToGoCipher[name]; ok {
			suites = append(suites, id)
		} else {
			log.Info("skipping unsupported cipher", "cipher", name)
		}
	}
	return suites
}

// MinTLSVersion converts an OpenShift TLSProtocolVersion string to the Go
// crypto/tls version constant. Returns tls.VersionTLS12 if the version is
// unrecognised.
func MinTLSVersion(v configv1.TLSProtocolVersion) uint16 {
	if ver, ok := protocolVersionMap[v]; ok {
		return ver
	}
	return tls.VersionTLS12
}

// opensslToIANA maps OpenSSL cipher names to IANA names used by kube-rbac-proxy
// and other Kubernetes components.
var opensslToIANA = map[string]string{
	"ECDHE-ECDSA-AES128-GCM-SHA256":  "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
	"ECDHE-RSA-AES128-GCM-SHA256":    "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
	"ECDHE-ECDSA-AES256-GCM-SHA384":  "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
	"ECDHE-RSA-AES256-GCM-SHA384":    "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
	"ECDHE-ECDSA-CHACHA20-POLY1305":  "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
	"ECDHE-RSA-CHACHA20-POLY1305":    "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
	"ECDHE-ECDSA-AES128-SHA256":      "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256",
	"ECDHE-RSA-AES128-SHA256":        "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256",
	"ECDHE-ECDSA-AES128-SHA":         "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA",
	"ECDHE-RSA-AES128-SHA":           "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA",
	"ECDHE-ECDSA-AES256-SHA":         "TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA",
	"ECDHE-RSA-AES256-SHA":           "TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA",
	"AES128-GCM-SHA256":              "TLS_RSA_WITH_AES_128_GCM_SHA256",
	"AES256-GCM-SHA384":              "TLS_RSA_WITH_AES_256_GCM_SHA384",
	"AES128-SHA256":                  "TLS_RSA_WITH_AES_128_CBC_SHA256",
	"AES128-SHA":                     "TLS_RSA_WITH_AES_128_CBC_SHA",
	"AES256-SHA":                     "TLS_RSA_WITH_AES_256_CBC_SHA",
	"DES-CBC3-SHA":                   "TLS_RSA_WITH_3DES_EDE_CBC_SHA",
}

// IANACipherSuites converts OpenSSL cipher names to IANA names suitable for
// kube-rbac-proxy --tls-cipher-suites and other Kubernetes TLS configuration.
// TLS 1.3 ciphers and unsupported ciphers are skipped.
func IANACipherSuites(opensslCiphers []string) []string {
	var suites []string
	for _, name := range opensslCiphers {
		if tls13Ciphers[name] {
			continue
		}
		if iana, ok := opensslToIANA[name]; ok {
			suites = append(suites, iana)
		}
	}
	return suites
}

// MinTLSVersionString returns the OpenShift TLSProtocolVersion string in a
// format suitable for kube-rbac-proxy --tls-min-version (e.g. "VersionTLS12").
func MinTLSVersionString(v configv1.TLSProtocolVersion) string {
	if v == "" {
		return string(configv1.VersionTLS12)
	}
	return string(v)
}

// NewTLSConfigApplicator returns a function suitable for use in
// controller-runtime's webhook.Options.TLSOpts or metrics server TLSOpts.
// It fetches the TLS profile from the API Server and applies MinVersion and
// CipherSuites to the provided tls.Config.
//
// If the profile cannot be fetched (e.g. running outside OpenShift), it falls
// back to the Intermediate profile defaults.
func NewTLSConfigApplicator(cfg *rest.Config) func(*tls.Config) {
	profile, err := GetAPIServerTLSProfile(cfg)
	if err != nil {
		log.Error(err, "failed to fetch API Server TLS profile, using Intermediate defaults")
	}

	spec := ResolveTLSProfileSpec(profile)
	minVersion := MinTLSVersion(spec.MinTLSVersion)
	cipherSuites := CipherSuites(spec.Ciphers)

	log.Info("resolved TLS profile",
		"minVersion", spec.MinTLSVersion,
		"cipherCount", len(cipherSuites),
		"ciphers", spec.Ciphers,
	)

	return func(c *tls.Config) {
		c.MinVersion = minVersion
		c.CipherSuites = cipherSuites
	}
}
