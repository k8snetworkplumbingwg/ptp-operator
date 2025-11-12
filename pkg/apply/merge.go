package apply

import (
	"context"
	"log"

	"github.com/pkg/errors"

	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// MergeMetadataForUpdate merges the read-only fields of metadata.
// This is to be able to do a a meaningful comparison in apply,
// since objects created on runtime do not have these fields populated.
func MergeMetadataForUpdate(current, updated *uns.Unstructured) error {
	updated.SetCreationTimestamp(current.GetCreationTimestamp())
	updated.SetSelfLink(current.GetSelfLink())
	updated.SetGeneration(current.GetGeneration())
	updated.SetUID(current.GetUID())
	updated.SetResourceVersion(current.GetResourceVersion())

	mergeAnnotations(current, updated)
	mergeLabels(current, updated)

	return nil
}

// MergeObjectForUpdate prepares a "desired" object to be updated.
// Some objects, such as Deployments and Services require
// some semantic-aware updates
func MergeObjectForUpdate(ctx context.Context, current, updated *uns.Unstructured) error {
	if err := MergeDeploymentForUpdate(current, updated); err != nil {
		return err
	}

	if err := MergeDaemonSetForUpdate(ctx, current, updated); err != nil {
		return err
	}

	if err := MergeServiceForUpdate(current, updated); err != nil {
		return err
	}

	if err := MergeServiceAccountForUpdate(current, updated); err != nil {
		return err
	}

	// For all object types, merge metadata.
	// Run this last, in case any of the more specific merge logic has
	// changed "updated"
	MergeMetadataForUpdate(current, updated)

	return nil
}

const (
	deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"
)

// MergeDeploymentForUpdate updates Deployment objects.
// We merge annotations, keeping ours except the Deployment Revision annotation.
func MergeDeploymentForUpdate(current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "apps" && gvk.Kind == "Deployment" {

		// Copy over the revision annotation from current up to updated
		// otherwise, updated would win, and this annotation is "special" and
		// needs to be preserved
		curAnnotations := current.GetAnnotations()
		updatedAnnotations := updated.GetAnnotations()
		if updatedAnnotations == nil {
			updatedAnnotations = map[string]string{}
		}

		anno, ok := curAnnotations[deploymentRevisionAnnotation]
		if ok {
			updatedAnnotations[deploymentRevisionAnnotation] = anno
		}

		updated.SetAnnotations(updatedAnnotations)
	}

	return nil
}

// MergeDaemonSetForUpdate merges DaemonSet templates using context to determine source:
// - PtpConfig controller: use all volumes/annotations/mounts from updated (even if empty)
// - PtpOperatorConfig controller: use base from updated + preserve security from current
// This supports adding/deleting sa_file or secrets while preserving non-security changes.
func MergeDaemonSetForUpdate(ctx context.Context, current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "apps" && gvk.Kind == "DaemonSet" {
		// Only apply to linuxptp-daemon DaemonSet
		if updated.GetName() != "linuxptp-daemon" {
			return nil
		}

		// Get controller source from context
		source, _ := ctx.Value(ControllerSourceKey).(string)
		log.Printf("MergeDaemonSet: Controller source from context: %s", source)

		// Determine if we should preserve security items from current
		preserveSecurityFromCurrent := (source == SourcePtpOperatorConfig)

		if err := mergeSecurityVolumes(current, updated, preserveSecurityFromCurrent); err != nil {
			return err
		}

		if err := mergeSecurityAnnotations(current, updated, preserveSecurityFromCurrent); err != nil {
			return err
		}

		if err := mergeSecurityVolumeMounts(current, updated, preserveSecurityFromCurrent); err != nil {
			return err
		}
	}

	return nil
}

// mergeSecurityVolumes implements simplified merge logic for volumes
// If preserveSecurityFromCurrent is true (PtpOperatorConfig), preserve security from current
// If false (PtpConfig), use security from updated (even if empty)
func mergeSecurityVolumes(current, updated *uns.Unstructured, preserveSecurityFromCurrent bool) error {
	// Get volumes from current
	currentVolumes, found, err := uns.NestedSlice(current.Object, "spec", "template", "spec", "volumes")
	if err != nil || !found {
		currentVolumes = []interface{}{}
	}

	// Get volumes from updated
	updatedVolumes, found, err := uns.NestedSlice(updated.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		return err
	}
	if !found {
		updatedVolumes = []interface{}{}
	}

	// Step 1: Get non-security volumes from updated (base template)
	var updatedNonSecurityVolumes []interface{}
	for _, vol := range updatedVolumes {
		volMap, ok := vol.(map[string]interface{})
		if !ok {
			updatedNonSecurityVolumes = append(updatedNonSecurityVolumes, vol)
			continue
		}
		name, ok := volMap["name"].(string)
		if !ok {
			updatedNonSecurityVolumes = append(updatedNonSecurityVolumes, vol)
			continue
		}

		// Skip security volumes
		if len(name) >= 14 && name[len(name)-14:] == "-ptpconfig-sec" {
			continue
		}
		updatedNonSecurityVolumes = append(updatedNonSecurityVolumes, vol)
	}

	// Step 2: Determine which security volumes to use
	var securityVolumesToUse []interface{}
	if preserveSecurityFromCurrent {
		// PtpOperatorConfig update: preserve security volumes from current
		for _, vol := range currentVolumes {
			volMap, ok := vol.(map[string]interface{})
			if !ok {
				continue
			}
			name, ok := volMap["name"].(string)
			if !ok {
				continue
			}
			if len(name) >= 14 && name[len(name)-14:] == "-ptpconfig-sec" {
				log.Printf("MergeDaemonSet: Preserving security volume from current: %s", name)
				securityVolumesToUse = append(securityVolumesToUse, vol)
			}
		}
	} else {
		// PtpConfig update: use security volumes from updated (even if empty)
		for _, vol := range updatedVolumes {
			volMap, ok := vol.(map[string]interface{})
			if !ok {
				continue
			}
			name, ok := volMap["name"].(string)
			if !ok {
				continue
			}
			if len(name) >= 14 && name[len(name)-14:] == "-ptpconfig-sec" {
				log.Printf("MergeDaemonSet: Using security volume from updated: %s", name)
				securityVolumesToUse = append(securityVolumesToUse, vol)
			}
		}
	}

	// Step 3: Merge = non-security from updated + security
	mergedVolumes := append(updatedNonSecurityVolumes, securityVolumesToUse...)
	log.Printf("MergeDaemonSet: Merged volumes: %d non-security + %d security = %d total",
		len(updatedNonSecurityVolumes), len(securityVolumesToUse), len(mergedVolumes))

	return uns.SetNestedSlice(updated.Object, mergedVolumes, "spec", "template", "spec", "volumes")
}

// mergeSecurityAnnotations implements simplified merge logic for annotations
// If preserveSecurityFromCurrent is true (PtpOperatorConfig), preserve security from current
// If false (PtpConfig), use security from updated (even if empty)
func mergeSecurityAnnotations(current, updated *uns.Unstructured, preserveSecurityFromCurrent bool) error {
	// Get annotations from current
	currentAnnotations, found, err := uns.NestedStringMap(current.Object, "spec", "template", "metadata", "annotations")
	if err != nil || !found {
		currentAnnotations = make(map[string]string)
	}

	// Get annotations from updated
	updatedAnnotations, found, err := uns.NestedStringMap(updated.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return err
	}
	if !found {
		updatedAnnotations = make(map[string]string)
	}

	// Step 1: Get non-security annotations from updated
	updatedNonSecurityAnnotations := make(map[string]string)
	securityPrefix := "ptp.openshift.io/secret-hash-"
	for k, v := range updatedAnnotations {
		if len(k) > len(securityPrefix) && k[:len(securityPrefix)] == securityPrefix {
			continue
		}
		updatedNonSecurityAnnotations[k] = v
	}

	// Step 2: Determine which security annotations to use
	securityAnnotationsToUse := make(map[string]string)
	if preserveSecurityFromCurrent {
		// PtpOperatorConfig update: preserve security annotations from current
		for k, v := range currentAnnotations {
			if len(k) > len(securityPrefix) && k[:len(securityPrefix)] == securityPrefix {
				log.Printf("MergeDaemonSet: Preserving security annotation from current: %s", k)
				securityAnnotationsToUse[k] = v
			}
		}
	} else {
		// PtpConfig update: use security annotations from updated (even if empty)
		for k, v := range updatedAnnotations {
			if len(k) > len(securityPrefix) && k[:len(securityPrefix)] == securityPrefix {
				log.Printf("MergeDaemonSet: Using security annotation from updated: %s", k)
				securityAnnotationsToUse[k] = v
			}
		}
	}

	// Step 3: Merge = non-security from updated + security
	mergedAnnotations := make(map[string]string)
	for k, v := range updatedNonSecurityAnnotations {
		mergedAnnotations[k] = v
	}
	for k, v := range securityAnnotationsToUse {
		mergedAnnotations[k] = v
	}

	log.Printf("MergeDaemonSet: Merged annotations: %d non-security + %d security = %d total",
		len(updatedNonSecurityAnnotations), len(securityAnnotationsToUse), len(mergedAnnotations))

	return uns.SetNestedStringMap(updated.Object, mergedAnnotations, "spec", "template", "metadata", "annotations")
}

// mergeSecurityVolumeMounts implements simplified merge logic for volume mounts
// If preserveSecurityFromCurrent is true (PtpOperatorConfig), preserve security from current
// If false (PtpConfig), use security from updated (even if empty)
func mergeSecurityVolumeMounts(current, updated *uns.Unstructured, preserveSecurityFromCurrent bool) error {
	// Get containers from current
	currentContainers, found, err := uns.NestedSlice(current.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		currentContainers = []interface{}{}
	}

	// Get containers from updated
	updatedContainers, found, err := uns.NestedSlice(updated.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		return err
	}

	// Step 1: Get non-security mounts from updated linuxptp-daemon-container
	var updatedNonSecurityMounts []interface{}
	for _, cont := range updatedContainers {
		contMap, ok := cont.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := contMap["name"].(string); ok && name == "linuxptp-daemon-container" {
			mounts, found, err := uns.NestedSlice(contMap, "volumeMounts")
			if err != nil || !found {
				break
			}
			for _, mount := range mounts {
				mountMap, ok := mount.(map[string]interface{})
				if !ok {
					updatedNonSecurityMounts = append(updatedNonSecurityMounts, mount)
					continue
				}
				mountName, ok := mountMap["name"].(string)
				if !ok {
					updatedNonSecurityMounts = append(updatedNonSecurityMounts, mount)
					continue
				}

				// Skip security mounts
				if len(mountName) >= 14 && mountName[len(mountName)-14:] == "-ptpconfig-sec" {
					continue
				}
				updatedNonSecurityMounts = append(updatedNonSecurityMounts, mount)
			}
			break
		}
	}

	// Step 2: Determine which security mounts to use
	var securityMountsToUse []interface{}
	if preserveSecurityFromCurrent {
		// PtpOperatorConfig update: preserve security mounts from current
		for _, cont := range currentContainers {
			contMap, ok := cont.(map[string]interface{})
			if !ok {
				continue
			}
			if name, ok := contMap["name"].(string); ok && name == "linuxptp-daemon-container" {
				mounts, found, err := uns.NestedSlice(contMap, "volumeMounts")
				if err != nil || !found {
					break
				}
				for _, mount := range mounts {
					mountMap, ok := mount.(map[string]interface{})
					if !ok {
						continue
					}
					mountName, ok := mountMap["name"].(string)
					if !ok {
						continue
					}
					if len(mountName) >= 14 && mountName[len(mountName)-14:] == "-ptpconfig-sec" {
						log.Printf("MergeDaemonSet: Preserving security mount from current: %s", mountName)
						securityMountsToUse = append(securityMountsToUse, mount)
					}
				}
				break
			}
		}
	} else {
		// PtpConfig update: use security mounts from updated (even if empty)
		for _, cont := range updatedContainers {
			contMap, ok := cont.(map[string]interface{})
			if !ok {
				continue
			}
			if name, ok := contMap["name"].(string); ok && name == "linuxptp-daemon-container" {
				mounts, found, err := uns.NestedSlice(contMap, "volumeMounts")
				if err != nil || !found {
					break
				}
				for _, mount := range mounts {
					mountMap, ok := mount.(map[string]interface{})
					if !ok {
						continue
					}
					mountName, ok := mountMap["name"].(string)
					if !ok {
						continue
					}
					if len(mountName) >= 14 && mountName[len(mountName)-14:] == "-ptpconfig-sec" {
						log.Printf("MergeDaemonSet: Using security mount from updated: %s", mountName)
						securityMountsToUse = append(securityMountsToUse, mount)
					}
				}
				break
			}
		}
	}

	// Step 3: Merge = non-security from updated + security
	mergedMounts := append(updatedNonSecurityMounts, securityMountsToUse...)
	log.Printf("MergeDaemonSet: Merged mounts: %d non-security + %d security = %d total",
		len(updatedNonSecurityMounts), len(securityMountsToUse), len(mergedMounts))

	// Update the linuxptp-daemon-container with merged mounts
	for i, cont := range updatedContainers {
		contMap, ok := cont.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := contMap["name"].(string); ok && name == "linuxptp-daemon-container" {
			contMap["volumeMounts"] = mergedMounts
			updatedContainers[i] = contMap
			break
		}
	}

	return uns.SetNestedSlice(updated.Object, updatedContainers, "spec", "template", "spec", "containers")
}

// MergeServiceForUpdate ensures the clusterip is never written to
func MergeServiceForUpdate(current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "" && gvk.Kind == "Service" {
		clusterIP, found, err := uns.NestedString(current.Object, "spec", "clusterIP")
		if err != nil {
			return err
		}

		if found {
			return uns.SetNestedField(updated.Object, clusterIP, "spec", "clusterIP")
		}
	}

	return nil
}

// MergeServiceAccountForUpdate copies secrets from current to updated.
// This is intended to preserve the auto-generated token.
// Right now, we just copy current to updated and don't support supplying
// any secrets ourselves.
func MergeServiceAccountForUpdate(current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "" && gvk.Kind == "ServiceAccount" {
		curSecrets, ok, err := uns.NestedSlice(current.Object, "secrets")
		if err != nil {
			return err
		}

		if ok {
			uns.SetNestedField(updated.Object, curSecrets, "secrets")
		}

		curImagePullSecrets, ok, err := uns.NestedSlice(current.Object, "imagePullSecrets")
		if err != nil {
			return err
		}
		if ok {
			uns.SetNestedField(updated.Object, curImagePullSecrets, "imagePullSecrets")
		}
	}
	return nil
}

// mergeAnnotations copies over any annotations from current to updated,
// with updated winning if there's a conflict
func mergeAnnotations(current, updated *uns.Unstructured) {
	updatedAnnotations := updated.GetAnnotations()
	curAnnotations := current.GetAnnotations()

	if curAnnotations == nil {
		curAnnotations = map[string]string{}
	}

	for k, v := range updatedAnnotations {
		curAnnotations[k] = v
	}

	updated.SetAnnotations(curAnnotations)
}

// mergeLabels copies over any labels from current to updated,
// with updated winning if there's a conflict
func mergeLabels(current, updated *uns.Unstructured) {
	updatedLabels := updated.GetLabels()
	curLabels := current.GetLabels()

	if curLabels == nil {
		curLabels = map[string]string{}
	}

	for k, v := range updatedLabels {
		curLabels[k] = v
	}

	updated.SetLabels(curLabels)
}

// IsObjectSupported rejects objects with configurations we don't support.
// This catches ServiceAccounts with secrets, which is valid but we don't
// support reconciling them.
func IsObjectSupported(obj *uns.Unstructured) error {
	gvk := obj.GroupVersionKind()

	// We cannot create ServiceAccounts with secrets because there's currently
	// no need and the merging logic is complex.
	// If you need this, please file an issue.
	if gvk.Group == "" && gvk.Kind == "ServiceAccount" {
		secrets, ok, err := uns.NestedSlice(obj.Object, "secrets")
		if err != nil {
			return err
		}

		if ok && len(secrets) > 0 {
			return errors.Errorf("cannot create ServiceAccount with secrets")
		}
	}

	return nil
}
