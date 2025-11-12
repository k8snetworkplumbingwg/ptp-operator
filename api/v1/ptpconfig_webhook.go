/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type PtpRole int

const (
	Master PtpRole = 1
	Slave  PtpRole = 0
)

// log is for logging in this package.
var ptpconfiglog = logf.Log.WithName("ptpconfig-resource")
var profileRegEx = regexp.MustCompile(`^([\w\-_]+)(,\s*([\w\-_]+))*$`)
var clockTypes = []string{"T-GM", "T-BC"}

// webhookClient is used by the webhook to query existing PtpConfigs
var webhookClient client.Client

func (r *PtpConfig) SetupWebhookWithManager(mgr ctrl.Manager) error {
	// Store the client for use in validation
	webhookClient = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/validate-ptp-openshift-io-v1-ptpconfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=ptp.openshift.io,resources=ptpconfigs,verbs=create;update,versions=v1,name=vptpconfig.kb.io,admissionReviewVersions=v1

type ptp4lConfSection struct {
	options map[string]string
}

type ptp4lConf struct {
	sections map[string]ptp4lConfSection
}

// Ptp4lConf is a public wrapper for ptp4lConf
type Ptp4lConf struct {
	conf ptp4lConf
}

// PopulatePtp4lConf parses the ptp4l configuration
func (p *Ptp4lConf) PopulatePtp4lConf(config *string, ptp4lopts *string) error {
	return p.conf.populatePtp4lConf(config, ptp4lopts)
}

// GetOption retrieves an option value from a specific section
func (p *Ptp4lConf) GetOption(section, key string) string {
	if sec, ok := p.conf.sections[section]; ok {
		if val, ok := sec.options[key]; ok {
			return val
		}
	}
	return ""
}

func (output *ptp4lConf) populatePtp4lConf(config *string, ptp4lopts *string) error {
	var string_config string
	if config != nil {
		string_config = *config
	}
	lines := strings.Split(string_config, "\n")
	var currentSection string
	output.sections = make(map[string]ptp4lConfSection)

	for _, line := range lines {
		if strings.HasPrefix(line, "[") {
			currentSection = line
			currentLine := strings.Split(line, "]")

			if len(currentLine) < 2 {
				return errors.New("Section missing closing ']'")
			}

			currentSection = fmt.Sprintf("%s]", currentLine[0])
			section := ptp4lConfSection{options: map[string]string{}}
			output.sections[currentSection] = section
		} else if currentSection != "" {
			split := strings.IndexByte(line, ' ')
			if split > 0 {
				section := output.sections[currentSection]
				section.options[line[:split]] = strings.TrimSpace(line[split+1:])
				output.sections[currentSection] = section
			}
		} else {
			return errors.New("Config option not in section")
		}
	}
	_, exist := output.sections["[global]"]
	if !exist {
		output.sections["[global]"] = ptp4lConfSection{options: map[string]string{}}
	}

	return nil
}

func (r *PtpConfig) validate() error {
	profiles := r.Spec.Profile

	for _, profile := range profiles {
		conf := &ptp4lConf{}
		conf.populatePtp4lConf(profile.Ptp4lConf, profile.Ptp4lOpts)

		// Validate that interface field only set in ordinary clock
		if profile.Interface != nil && *profile.Interface != "" {
			for section := range conf.sections {
				if section != "[global]" {
					if section != ("[" + *profile.Interface + "]") {
						return errors.New("interface section " + section + " not allowed when specifying interface section")
					}
				}
			}
		}

		if profile.PtpSchedulingPolicy != nil && *profile.PtpSchedulingPolicy == "SCHED_FIFO" {
			if profile.PtpSchedulingPriority == nil {
				return errors.New("PtpSchedulingPriority must be set for SCHED_FIFO PtpSchedulingPolicy")
			}
		}

		if profile.PtpSettings != nil {
			for k, v := range profile.PtpSettings {
				switch {
				case k == "stdoutFilter":
					_, err := regexp.Compile(v)
					if err != nil {
						return errors.New("stdoutFilter='" + v + "' is invalid; " + err.Error())
					}
				case k == "logReduce":
					logReduceMode := "false"
					logReduceSettings := strings.Fields(v)
					if len(logReduceSettings) >= 1 {
						logReduceMode = strings.ToLower(logReduceSettings[0])
					}
					if logReduceMode != "true" && logReduceMode != "false" && logReduceMode != "basic" && logReduceMode != "enhanced" {
						return errors.New("logReduce mode '" + logReduceMode + "' is invalid; mode must be in 'true', 'false, 'basic', or 'enhanced'")
					}
					if logReduceMode == "enhanced" {
						if len(logReduceSettings) >= 2 {
							if _, err := time.ParseDuration(logReduceSettings[1]); err != nil {
								return errors.New("logReduce time " + logReduceSettings[1] + "' is invalid; must be a valid time duration (e.g. '30s')")
							}
						}
						if len(logReduceSettings) >= 3 {
							if threshold, err := strconv.Atoi(logReduceSettings[2]); err != nil || threshold < 0 {
								return errors.New("logReduce threshold " + logReduceSettings[2] + "' is invalid; must be a non-negative integer")
							}
						}
					}
				case k == "haProfiles":
					if !profileRegEx.MatchString(v) {
						return errors.New("haProfiles='" + v + "' is invalid; must be comma seperated profile names")
					}
				case k == "clockType":
					if !slices.Contains(clockTypes, v) {
						return errors.New("clockType='" + v + "' is invalid; must be one of ['" + strings.Join(clockTypes, "', '") + "']")
					}
				case k == "inSyncConditionTimes":
					// Validate inSyncConditionTimes is an unsigned integer
					if _, err := strconv.ParseUint(v, 10, 32); err != nil {
						return errors.New("inSyncConditionTimes='" + v + "' is invalid; must be an unsigned integer")
					}
				case k == "inSyncConditionThreshold":
					// Validate inSyncConditionThreshold is an unsigned integer
					if _, err := strconv.ParseUint(v, 10, 32); err != nil {
						return errors.New("inSyncConditionThreshold='" + v + "' is invalid; must be an unsigned integer")
					}

				case strings.Contains(k, "clockId"):
					// Allow explicit clockId
					if _, err := strconv.ParseUint(v, 10, 64); err != nil {
						if _, err := strconv.ParseUint(v, 16, 64); err != nil {
							return errors.New("clockId='" + v + "' is invalid; must be an unsigned integer")
						}
					}
				case k == "controllingProfile":
					// Allow controllingProfile setting - no specific validation required for string
				case k == "upstreamPort":
					// Temporary allow upstreamPort setting - no specific validation required for string
				case k == "leadingInterface":
					// Temporary allow leadingInterface setting - no specific validation required for string
				default:
					return errors.New("profile.PtpSettings '" + k + "' is not a configurable setting")
				}
			}
		}
	}
	return nil
}

// validateSecretConflicts checks if this PtpConfig's sa_file + secret combination
// conflicts with any existing PtpConfigs in the openshift-ptp namespace
func (r *PtpConfig) validateSecretConflicts(ctx context.Context) error {
	if webhookClient == nil {
		ptpconfiglog.Info("webhook client not initialized, skipping cross-PtpConfig validation")
		return nil
	}

	// Build map of sa_file -> secret for THIS PtpConfig
	currentSaFileToSecret := make(map[string]string)
	for _, profile := range r.Spec.Profile {
		if profile.PtpSecretName == nil || *profile.PtpSecretName == "" {
			continue
		}
		if profile.Ptp4lConf == nil {
			continue
		}

		conf := &ptp4lConf{}
		if err := conf.populatePtp4lConf(profile.Ptp4lConf, profile.Ptp4lOpts); err != nil {
			continue
		}

		if globalSection, exists := conf.sections["[global]"]; exists {
			if saFile, exists := globalSection.options["sa_file"]; exists && saFile != "" {
				currentSaFileToSecret[saFile] = *profile.PtpSecretName
			}
		}
	}

	// If this PtpConfig doesn't use any secrets, no conflict possible
	if len(currentSaFileToSecret) == 0 {
		return nil
	}

	// List all existing PtpConfigs in openshift-ptp namespace
	ptpConfigList := &PtpConfigList{}
	if err := webhookClient.List(ctx, ptpConfigList, &client.ListOptions{
		Namespace: "openshift-ptp",
	}); err != nil {
		ptpconfiglog.Error(err, "failed to list PtpConfigs for validation")
		// Don't block creation if we can't list - fail open
		return nil
	}

	// Check each existing PtpConfig
	for _, existingConfig := range ptpConfigList.Items {
		// Skip checking against ourselves (for updates)
		if existingConfig.Name == r.Name && existingConfig.Namespace == r.Namespace {
			continue
		}

		// Check each profile in the existing config
		for _, profile := range existingConfig.Spec.Profile {
			if profile.PtpSecretName == nil || *profile.PtpSecretName == "" {
				continue
			}
			if profile.Ptp4lConf == nil {
				continue
			}

			conf := &ptp4lConf{}
			if err := conf.populatePtp4lConf(profile.Ptp4lConf, profile.Ptp4lOpts); err != nil {
				continue
			}

			if globalSection, exists := conf.sections["[global]"]; exists {
				if saFile, exists := globalSection.options["sa_file"]; exists && saFile != "" {
					// Check if THIS PtpConfig uses the same sa_file
					if currentSecret, found := currentSaFileToSecret[saFile]; found {
						// Conflict: same sa_file but different secret
						if currentSecret != *profile.PtpSecretName {
							return fmt.Errorf("sa_file '%s' conflict: PtpConfig '%s' already uses secret '%s' with this sa_file path, but this PtpConfig tries to use secret '%s'. All PtpConfigs using the same sa_file must reference the same secret",
								saFile, existingConfig.Name, *profile.PtpSecretName, currentSecret)
						}
					}
				}
			}
		}
	}

	return nil
}

// validateSecretExists checks that ptpSecretName references an existing secret
func (r *PtpConfig) validateSecretExists(ctx context.Context) error {
	if webhookClient == nil {
		ptpconfiglog.Info("webhook client not initialized, skipping secret existence validation")
		return nil
	}

	for _, profile := range r.Spec.Profile {
		// Skip if no secret specified
		if profile.PtpSecretName == nil || *profile.PtpSecretName == "" {
			continue
		}

		secretName := *profile.PtpSecretName
		profileName := "unknown"
		if profile.Name != nil {
			profileName = *profile.Name
		}

		// Try to get the secret from openshift-ptp namespace
		secret := &corev1.Secret{}
		err := webhookClient.Get(ctx, types.NamespacedName{
			Namespace: "openshift-ptp",
			Name:      secretName,
		}, secret)

		if err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("secret '%s' referenced by profile '%s' does not exist in namespace 'openshift-ptp'. Please create the secret before referencing it in PtpConfig",
					secretName, profileName)
			}
			// For other errors (like permission issues), log but don't block
			ptpconfiglog.Error(err, "failed to verify secret existence", "secret", secretName, "profile", profileName)
			// Fail open - don't block if we can't verify
			return nil
		}

		ptpconfiglog.Info("validated secret exists", "secret", secretName, "profile", profileName)

		// Validate SPP (Security Parameter Profile) if specified
		if err := r.validateSppInSecret(profile, secret); err != nil {
			return err
		}
	}

	return nil
}

// validateSppInSecret checks that the spp value in ptp4lConf exists in the referenced secret
func (r *PtpConfig) validateSppInSecret(profile PtpProfile, secret *corev1.Secret) error {
	// Skip if no ptp4lConf
	if profile.Ptp4lConf == nil {
		return nil
	}

	// Parse ptp4lConf to get spp value from [global] section
	conf := &ptp4lConf{}
	if err := conf.populatePtp4lConf(profile.Ptp4lConf, profile.Ptp4lOpts); err != nil {
		// If we can't parse the config, skip validation (other validations will catch this)
		return nil
	}

	globalSection, exists := conf.sections["[global]"]
	if !exists {
		return nil
	}

	sppValue, exists := globalSection.options["spp"]
	if !exists || sppValue == "" {
		// No spp specified, nothing to validate
		return nil
	}

	profileName := "unknown"
	if profile.Name != nil {
		profileName = *profile.Name
	}

	// Parse the secret data to find valid SPP values
	validSpps, err := parseValidSppsFromSecret(secret)
	if err != nil {
		ptpconfiglog.Error(err, "failed to parse SPPs from secret", "secret", secret.Name, "profile", profileName)
		// Fail open - don't block if we can't parse
		return nil
	}

	// Check if the specified SPP exists in the secret
	if !contains(validSpps, sppValue) {
		return fmt.Errorf("spp '%s' in profile '%s' is not defined in secret '%s'. Valid SPPs in secret: %v",
			sppValue, profileName, secret.Name, validSpps)
	}

	ptpconfiglog.Info("validated spp exists in secret", "spp", sppValue, "secret", secret.Name, "profile", profileName)
	return nil
}

// parseValidSppsFromSecret extracts all valid SPP numbers from a PTP security secret
// It looks for any line starting with "spp <number>" regardless of structure or sections
func parseValidSppsFromSecret(secret *corev1.Secret) ([]string, error) {
	var validSpps []string

	// Iterate through all keys in the secret
	for key, value := range secret.Data {
		// Parse the security configuration
		content := string(value)
		lines := strings.Split(content, "\n")

		// Look for any line starting with "spp <number>"
		for _, line := range lines {
			line = strings.TrimSpace(line)

			// Skip empty lines and comments
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			// Check if line starts with "spp " (case-insensitive)
			if strings.HasPrefix(strings.ToLower(line), "spp ") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					sppNumber := parts[1]
					// Add if not already in list
					if !contains(validSpps, sppNumber) {
						validSpps = append(validSpps, sppNumber)
					}
				}
			}
		}

		ptpconfiglog.Info("parsed SPPs from secret", "key", key, "spps", validSpps)
	}

	return validSpps, nil
}

// contains checks if a string slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

var _ webhook.Validator = &PtpConfig{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *PtpConfig) ValidateCreate() (admission.Warnings, error) {
	ptpconfiglog.Info("validate create", "name", r.Name)
	if err := r.validate(); err != nil {
		return admission.Warnings{}, err
	}

	// Check that referenced secrets exist
	if err := r.validateSecretExists(context.Background()); err != nil {
		return admission.Warnings{}, err
	}

	// Check for cross-PtpConfig secret conflicts
	if err := r.validateSecretConflicts(context.Background()); err != nil {
		return admission.Warnings{}, err
	}

	return admission.Warnings{}, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *PtpConfig) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	ptpconfiglog.Info("validate update", "name", r.Name)
	if err := r.validate(); err != nil {
		return admission.Warnings{}, err
	}

	// Check that referenced secrets exist
	if err := r.validateSecretExists(context.Background()); err != nil {
		return admission.Warnings{}, err
	}

	// Check for cross-PtpConfig secret conflicts
	if err := r.validateSecretConflicts(context.Background()); err != nil {
		return admission.Warnings{}, err
	}

	return admission.Warnings{}, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *PtpConfig) ValidateDelete() (admission.Warnings, error) {
	ptpconfiglog.Info("validate delete", "name", r.Name)
	return admission.Warnings{}, nil
}

func getInterfaces(input *ptp4lConf, mode PtpRole) (interfaces []string) {

	for index, section := range input.sections {
		sectionName := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(index, "[", ""), "]", ""))
		if strings.TrimSpace(section.options["masterOnly"]) == strconv.Itoa(int(mode)) {
			interfaces = append(interfaces, strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(sectionName, "[", ""), "]", "")))
		}
	}
	return interfaces
}

func GetInterfaces(config PtpConfig, mode PtpRole) (interfaces []string) {

	if len(config.Spec.Profile) > 1 {
		logrus.Warnf("More than one profile detected for ptpconfig %s", config.ObjectMeta.Name)
	}
	if len(config.Spec.Profile) == 0 {
		logrus.Warnf("No profile detected for ptpconfig %s", config.ObjectMeta.Name)
		return interfaces
	}
	conf := &ptp4lConf{}
	var dummy *string
	err := conf.populatePtp4lConf(config.Spec.Profile[0].Ptp4lConf, dummy)
	if err != nil {
		logrus.Warnf("ptp4l conf parsing failed, err=%s", err)
	}

	interfaces = getInterfaces(conf, mode)
	var finalInterfaces []string
	for _, aIf := range interfaces {
		if aIf == "global" {
			if config.Spec.Profile[0].Interface != nil {
				finalInterfaces = append(finalInterfaces, *config.Spec.Profile[0].Interface)
			}
		} else {
			finalInterfaces = append(finalInterfaces, aIf)
		}
	}
	if len(interfaces) == 0 && mode == Slave && config.Spec.Profile[0].Interface != nil {
		finalInterfaces = append(finalInterfaces, *config.Spec.Profile[0].Interface)
	}
	return finalInterfaces
}
