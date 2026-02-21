package controllers

import (
	"fmt"
	"sort"
	"strings"

	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
)

// qualifyProfileName creates a node-unique profile name by prepending the PtpConfig CR name.
// For example, CR "T-BC" with profile "maestro" becomes "T-BC/maestro".
// This is used to ensure that the profile name is unique across all PtpConfig CRs.
func qualifyProfileName(crName, profileName string) string {
	return crName + "/" + profileName
}

// profileCRIndex maps each profile name to the list of CR names that define it.
// Built once in getRecommendProfiles and used for O(1) lookups when qualifying
// cross-profile references (controllingProfile, haProfiles).
type profileCRIndex map[string][]string

// buildProfileCRIndex scans all PtpConfig CRs and records which CR(s) define each profile name.
func buildProfileCRIndex(ptpConfigList *ptpv1.PtpConfigList) profileCRIndex {
	idx := make(profileCRIndex)
	for _, cfg := range ptpConfigList.Items {
		if cfg.Spec.Profile == nil {
			continue
		}
		for _, p := range cfg.Spec.Profile {
			if p.Name != nil {
				idx[*p.Name] = append(idx[*p.Name], cfg.Name)
			}
		}
	}
	return idx
}

// resolveCR returns the CR name for a profile reference, preferring the current CR.
// If the profile is defined in multiple CRs and none is the current CR, a warning is logged.
func (idx profileCRIndex) resolveCR(profileName, currentCRName string) string {
	crNames := idx[profileName]
	if len(crNames) == 0 {
		return ""
	}
	for _, name := range crNames {
		if name == currentCRName {
			return currentCRName
		}
	}
	if len(crNames) > 1 {
		glog.Warningf("Ambiguous cross-profile reference: profile '%s' is defined in multiple CRs %v, using '%s'", profileName, crNames, crNames[0])
	}
	return crNames[0]
}

// qualifyCrossProfileReferences updates controllingProfile and haProfiles settings
// to use qualified names so cross-profile references remain valid after prefixing.
func qualifyCrossProfileReferences(settings map[string]string, currentCRName string, idx profileCRIndex) {
	if cp, ok := settings["controllingProfile"]; ok && cp != "" {
		if crName := idx.resolveCR(cp, currentCRName); crName != "" {
			settings["controllingProfile"] = qualifyProfileName(crName, cp)
		}
	}
	if ha, ok := settings["haProfiles"]; ok && ha != "" {
		parts := strings.Split(ha, ",")
		for i, p := range parts {
			p = strings.TrimSpace(p)
			if crName := idx.resolveCR(p, currentCRName); crName != "" {
				parts[i] = qualifyProfileName(crName, p)
			}
		}
		settings["haProfiles"] = strings.Join(parts, ",")
	}
}

func printWhenNotNil(p interface{}, description string) {
	switch v := p.(type) {
	case *string:
		if v != nil {
			glog.Info(description, ": ", *v)
		}
	case *int64:
		if v != nil {
			glog.Info(description, ": ", *v)
		}
	default:
		glog.Info(description, ": ", v)
	}
}

// getRecommendNodePtpProfiles return recommended node ptp profile
func getRecommendNodePtpProfiles(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) ([]ptpv1.PtpProfile, error) {
	glog.V(2).Infof("in getRecommendNodePtpProfiles")

	profiles, err := getRecommendProfiles(ptpConfigList, node)
	if err != nil {
		return nil, fmt.Errorf("get recommended ptp profiles failed: %v", err)
	}

	glog.Infof("ptp profiles to be updated for node: %s", node.Name)
	for _, profile := range profiles {
		glog.Infof("------------------------------------")
		printWhenNotNil(profile.Name, "Profile Name")
		printWhenNotNil(profile.Interface, "Interface")
		printWhenNotNil(profile.Ptp4lOpts, "Ptp4lOpts")
		printWhenNotNil(profile.Phc2sysOpts, "Phc2sysOpts")
		printWhenNotNil(profile.Ptp4lConf, "Ptp4lConf")
		printWhenNotNil(profile.PtpSchedulingPolicy, "PtpSchedulingPolicy")
		printWhenNotNil(profile.PtpSchedulingPriority, "PtpSchedulingPriority")
		glog.Infof("------------------------------------")
	}

	return profiles, nil
}

func getRecommendProfiles(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) ([]ptpv1.PtpProfile, error) {
	glog.V(2).Infof("In getRecommendProfiles")

	profilesNames := getRecommendProfilesNames(ptpConfigList, node)
	glog.V(2).Infof("recommended ptp profiles names are %v for node: %s", returnMapKeys(profilesNames), node.Name)

	idx := buildProfileCRIndex(ptpConfigList)

	profiles := []ptpv1.PtpProfile{}
	foundNames := make(map[string]bool)
	for _, cfg := range ptpConfigList.Items {
		if cfg.Spec.Profile == nil {
			continue
		}
		for _, profile := range cfg.Spec.Profile {
			if profile.Name == nil {
				continue
			}
			if _, exist := profilesNames[*profile.Name]; !exist {
				continue
			}
			foundNames[*profile.Name] = true
			profileCopy := profile.DeepCopy()
			qualifiedName := qualifyProfileName(cfg.Name, *profile.Name)
			profileCopy.Name = &qualifiedName

			if profileCopy.PtpSettings != nil {
				qualifyCrossProfileReferences(profileCopy.PtpSettings, cfg.Name, idx)
			}

			profiles = append(profiles, *profileCopy)
		}
	}

	if len(foundNames) != len(profilesNames) {
		return nil, fmt.Errorf("Failed to find all the recommended profiles")
	}
	// sort profiles by name
	sort.SliceStable(profiles, func(i, j int) bool {
		return *profiles[i].Name < *profiles[j].Name
	})

	return profiles, nil
}

func getRecommendProfilesNames(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) map[string]interface{} {
	glog.V(2).Infof("In getRecommendProfilesNames")

	var (
		allRecommend []ptpv1.PtpRecommend
	)

	// append recommend section from each custom resource into one list
	for _, cfg := range ptpConfigList.Items {
		if cfg.Spec.Recommend != nil {
			allRecommend = append(allRecommend, cfg.Spec.Recommend...)
		}
	}

	// allRecommend sorted by priority
	// priority 0 will become the first item in allRecommend
	sort.Slice(allRecommend, func(i, j int) bool {
		if allRecommend[i].Priority != nil && allRecommend[j].Priority != nil {
			return *allRecommend[i].Priority < *allRecommend[j].Priority
		}
		return allRecommend[i].Priority != nil
	})

	// Add all the profiles with the same priority
	profilesNames := make(map[string]interface{})
	foundPolicy := false
	priority := int64(-1)

	// loop allRecommend from high priority(0) to low(*)
	for _, r := range allRecommend {

		// ignore if profile not define in recommend
		if r.Profile == nil {
			continue
		}

		// ignore if match section is empty
		if len(r.Match) == 0 {
			continue
		}

		// check if the policy match the node
		switch {
		case !nodeMatches(&node, r.Match):
			continue
		case !foundPolicy:
			profilesNames[*r.Profile] = struct{}{}
			priority = *r.Priority
			foundPolicy = true
		case *r.Priority == priority:
			profilesNames[*r.Profile] = struct{}{}
		default:
			break
		}
	}

	return profilesNames
}

func nodeMatches(node *corev1.Node, matchRuleList []ptpv1.MatchRule) bool {
	// loop over Match list
	for _, m := range matchRuleList {

		// nodeName has higher priority than nodeLabel
		// return immediately if nodeName matches
		// make sure m.NodeName pointer is not nil before
		// comparing values
		if m.NodeName != nil && *m.NodeName == node.Name {
			return true
		}

		// return immediately when label matches
		// this makes sure priority field is respected
		for k := range node.Labels {
			// make sure m.NodeLabel pointer is not nil before
			// comparing values
			if m.NodeLabel != nil && *m.NodeLabel == k {
				return true
			}
		}
	}

	return false
}

func returnMapKeys(profiles map[string]interface{}) []string {
	keys := make([]string, len(profiles))

	i := 0
	for k := range profiles {
		keys[i] = k
		i++
	}

	return keys
}
