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

package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/go-logr/logr"
	"github.com/golang/glog"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/apply"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// PtpConfigReconciler reconciles a PtpConfig object
type PtpConfigReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpconfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;update;patch

func (r *PtpConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	reqLogger := r.Log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Reconciling PtpConfig")

	instances := &ptpv1.PtpConfigList{}
	err := r.List(ctx, instances, &client.ListOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	nodeList := &corev1.NodeList{}
	err = r.List(ctx, nodeList, &client.ListOptions{})
	if err != nil {
		glog.Errorf("failed to list nodes")
		return reconcile.Result{}, err
	}

	if err = r.syncPtpConfig(ctx, instances, nodeList); err != nil {
		return reconcile.Result{}, err
	}

	// After syncing ConfigMap, update DaemonSet with secret mounts
	if err = r.syncLinuxptpDaemonSecrets(ctx, instances); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// syncPtpConfig synchronizes PtpConfig CR
func (r *PtpConfigReconciler) syncPtpConfig(ctx context.Context, ptpConfigList *ptpv1.PtpConfigList, nodeList *corev1.NodeList) error {
	var err error

	nodePtpConfigMap := &corev1.ConfigMap{}
	nodePtpConfigMap.Name = names.DefaultPTPConfigMapName
	nodePtpConfigMap.Namespace = names.Namespace
	nodePtpConfigMap.Data = make(map[string]string)

	// Also update PTP config status with match list
	for _, ptpConfig := range ptpConfigList.Items {
		var matchList []ptpv1.NodeMatchList

		for _, node := range nodeList.Items {
			nodePtpProfiles, err := getRecommendNodePtpProfilesForConfig(&ptpConfig, node)
			if err != nil {
				glog.Errorf("failed to get recommended profiles for node %s: %v", node.Name, err)
				continue
			}

			// If this PTP config recommends profiles for this node, add to match list
			if len(nodePtpProfiles) > 0 {
				for _, profile := range nodePtpProfiles {
					matchList = append(matchList, ptpv1.NodeMatchList{
						NodeName: &node.Name,
						Profile:  profile.Name,
					})
				}
			}
		}

		// Update PTP config status if it has changed
		if !reflect.DeepEqual(ptpConfig.Status.MatchList, matchList) {
			ptpConfig.Status.MatchList = matchList
			err = r.Status().Update(ctx, &ptpConfig)
			if err != nil {
				glog.Errorf("failed to update PTP config status for %s: %v", ptpConfig.Name, err)
			} else {
				glog.Infof("updated PTP config status for %s with %d matches", ptpConfig.Name, len(matchList))
			}
		}
	}

	for _, node := range nodeList.Items {
		nodePtpProfiles, err := getRecommendNodePtpProfiles(ptpConfigList, node)
		if err != nil {
			return fmt.Errorf("failed to get recommended node PtpConfig: %v", err)
		}

		data, err := json.Marshal(nodePtpProfiles)
		if err != nil {
			return fmt.Errorf("failed to Marshal nodePtpProfiles: %v", err)
		}
		nodePtpConfigMap.Data[node.Name] = string(data)
	}

	cm := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace, Name: names.DefaultPTPConfigMapName}, cm)
	if err != nil {
		return fmt.Errorf("failed to get ptp config map: %v", err)
	} else {
		glog.Infof("ptp config map already exists, updating")
		cm.Data = nodePtpConfigMap.Data
		err = r.Update(ctx, cm)
		if err != nil {
			return fmt.Errorf("failed to update ptp config map: %v", err)
		}
	}
	return nil
}

// getRecommendNodePtpProfilesForConfig returns recommended PTP profiles for a node from a single PTP config
func getRecommendNodePtpProfilesForConfig(ptpConfig *ptpv1.PtpConfig, node corev1.Node) ([]ptpv1.PtpProfile, error) {
	profilesNames := getRecommendProfilesNamesForConfig(ptpConfig, node)
	if len(profilesNames) == 0 {
		return []ptpv1.PtpProfile{}, nil
	}

	profiles := []ptpv1.PtpProfile{}
	if ptpConfig.Spec.Profile != nil {
		for _, profile := range ptpConfig.Spec.Profile {
			if _, exist := profilesNames[*profile.Name]; exist {
				profiles = append(profiles, profile)
			}
		}
	}

	return profiles, nil
}

// getRecommendProfilesNamesForConfig returns recommended profile names for a node from a single PTP config
func getRecommendProfilesNamesForConfig(ptpConfig *ptpv1.PtpConfig, node corev1.Node) map[string]interface{} {
	var (
		allRecommend []ptpv1.PtpRecommend
	)

	// Get recommend section from this PTP config
	if ptpConfig.Spec.Recommend != nil {
		allRecommend = append(allRecommend, ptpConfig.Spec.Recommend...)
	}

	// Sort by priority (lower numbers have higher priority)
	sort.Slice(allRecommend, func(i, j int) bool {
		if allRecommend[i].Priority != nil && allRecommend[j].Priority != nil {
			return *allRecommend[i].Priority < *allRecommend[j].Priority
		}
		return allRecommend[i].Priority != nil
	})

	// Find matching profiles
	profilesNames := make(map[string]interface{})
	foundPolicy := false
	priority := int64(-1)

	// Loop through recommendations from high priority (0) to low (*)
	for _, r := range allRecommend {
		// Ignore if profile not defined in recommend
		if r.Profile == nil {
			continue
		}

		// Ignore if match section is empty
		if len(r.Match) == 0 {
			continue
		}

		// Check if the policy matches the node
		switch {
		case !ptpNodeMatches(&node, r.Match):
			continue
		case !foundPolicy:
			profilesNames[*r.Profile] = struct{}{}
			priority = *r.Priority
			foundPolicy = true
		case *r.Priority == priority:
			profilesNames[*r.Profile] = struct{}{}
		default:

		}
	}

	return profilesNames
}

// ptpNodeMatches checks if a node matches the given match rules for PTP config
func ptpNodeMatches(node *corev1.Node, matchRuleList []ptpv1.MatchRule) bool {
	// Loop over Match list
	for _, m := range matchRuleList {
		// NodeName has higher priority than nodeLabel
		// Return immediately if nodeName matches
		if m.NodeName != nil && *m.NodeName == node.Name {
			return true
		}

		// Return immediately when label matches
		for k := range node.Labels {
			if m.NodeLabel != nil && *m.NodeLabel == k {
				return true
			}
		}
	}

	return false
}

func (r *PtpConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ptpv1.PtpConfig{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetNamespace() == names.Namespace
		}))).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapSecretToPtpConfig),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
				return object.GetNamespace() == names.Namespace
			})),
		).
		Complete(r)
}

// mapSecretToPtpConfig maps secret changes to PtpConfig reconciliation requests
func (r *PtpConfigReconciler) mapSecretToPtpConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	// Only process secrets in the openshift-ptp namespace
	if secret.Namespace != names.Namespace {
		return nil
	}

	glog.Infof("Secret '%s' changed, checking if it's referenced by any PtpConfig", secret.Name)

	// List all PtpConfigs to find which ones reference this secret
	ptpConfigList := &ptpv1.PtpConfigList{}
	if err := r.List(ctx, ptpConfigList, &client.ListOptions{Namespace: names.Namespace}); err != nil {
		glog.Errorf("Failed to list PtpConfigs: %v", err)
		return nil
	}

	// Check if any PtpConfig references this secret
	secretReferenced := false
	for _, cfg := range ptpConfigList.Items {
		for _, profile := range cfg.Spec.Profile {
			if profile.PtpSecretName != nil && *profile.PtpSecretName == secret.Name {
				secretReferenced = true
				glog.Infof("Secret '%s' is referenced by PtpConfig '%s'", secret.Name, cfg.Name)
				break
			}
		}
		if secretReferenced {
			break
		}
	}

	if !secretReferenced {
		return nil
	}

	// Trigger reconciliation of all PtpConfigs (they all share the same DaemonSet)
	var requests []reconcile.Request
	for _, cfg := range ptpConfigList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      cfg.Name,
				Namespace: cfg.Namespace,
			},
		})
	}

	glog.Infof("Triggering reconciliation of %d PtpConfigs due to secret change", len(requests))
	return requests
}

// secretMount represents a secret and sa_file pair for a profile
type secretMount struct {
	secretName string
	saFilePath string
	secretHash string
	secretKey  string // The actual key name in the secret
}

// syncLinuxptpDaemonSecrets updates the linuxptp-daemon DaemonSet with secret volume mounts
func (r *PtpConfigReconciler) syncLinuxptpDaemonSecrets(ctx context.Context, ptpConfigList *ptpv1.PtpConfigList) error {
	// 1. Collect {secretName, sa_file} pairs from each profile
	var mounts []secretMount

	glog.Info("Scanning PtpConfigs for {secretName, sa_file} pairs...")
	for _, cfg := range ptpConfigList.Items {
		for _, profile := range cfg.Spec.Profile {
			profileName := "unknown"
			if profile.Name != nil {
				profileName = *profile.Name
			}

			// Check if profile has both PtpSecretName and sa_file
			if profile.PtpSecretName == nil || *profile.PtpSecretName == "" {
				continue
			}
			if profile.Ptp4lConf == nil {
				continue
			}

			secretName := *profile.PtpSecretName

			// Parse ptp4lConf to get sa_file
			conf := &ptpv1.Ptp4lConf{}
			if err := conf.PopulatePtp4lConf(profile.Ptp4lConf, profile.Ptp4lOpts); err != nil {
				glog.Warningf("Failed to parse ptp4lConf for profile %s: %v", profileName, err)
				continue
			}

			// Get sa_file from [global] section
			saFilePath := conf.GetOption("[global]", "sa_file")
			if saFilePath == "" {
				continue
			}

			glog.Infof("Found {secret, sa_file} pair in PtpConfig %s, profile %s: {%s, %s}",
				cfg.Name, profileName, secretName, saFilePath)

			// Load secret and compute hash
			sec := &corev1.Secret{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: names.Namespace, Name: secretName}, sec); err != nil {
				glog.Errorf("Failed to load secret %s for profile %s: %v", secretName, profileName, err)
				continue
			}

			// Check secret has data
			if len(sec.Data) == 0 {
				glog.Warningf("Secret %s for profile %s has no data keys", secretName, profileName)
				continue
			}

			// Get the first key from the secret (we'll use this in the volume items field)
			var secretKey string
			for k := range sec.Data {
				secretKey = k
				break
			}

			if len(sec.Data) > 1 {
				glog.Infof("Secret %s for profile %s has multiple keys (%d total) - using key '%s'",
					secretName, profileName, len(sec.Data), secretKey)
			}

			// Compute hash
			secretHash := computeSecretHash(sec)
			glog.Infof("Computed secret hash for '%s': %s", secretName, secretHash)

			mounts = append(mounts, secretMount{
				secretName: secretName,
				saFilePath: saFilePath,
				secretHash: secretHash,
				secretKey:  secretKey,
			})
		}
	}

	// Deduplicate by volume name (multiple PtpConfigs may use same secret+sa_file)
	uniqueMounts := make(map[string]secretMount)
	for _, mount := range mounts {
		// Derive volume name (same logic as injectPtpSecurityVolume)
		filename := filepath.Base(mount.saFilePath)
		nameWithoutExt := filename
		if lastDot := lastIndexByte(filename, '.'); lastDot >= 0 {
			nameWithoutExt = filename[:lastDot]
		}
		volumeName := nameWithoutExt + "-ptpconfig-sec"

		// Only keep first occurrence (they all have same secret+sa_file anyway)
		if _, exists := uniqueMounts[volumeName]; !exists {
			uniqueMounts[volumeName] = mount
		}
	}

	// Convert back to slice
	mounts = nil
	for _, mount := range uniqueMounts {
		mounts = append(mounts, mount)
	}

	glog.Infof("Found %d unique secret mount(s) to apply (deduplicated from %d total)", len(mounts), len(uniqueMounts))

	// Always update DaemonSet, even if mounts is empty
	// This ensures volumes are removed when sa_file is deleted from PtpConfigs

	// 2. Get the linuxptp-daemon DaemonSet
	daemonSet := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace,
		Name:      "linuxptp-daemon",
	}, daemonSet)
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Info("linuxptp-daemon DaemonSet not found yet - will retry in 10 seconds")
			// Requeue to retry after DaemonSet is created
			return fmt.Errorf("DaemonSet not found, will retry")
		}
		return fmt.Errorf("failed to get linuxptp-daemon DaemonSet: %v", err)
	}

	// 3. Remove all old security volumes first
	removeSecurityVolumesFromDaemonSet(daemonSet)

	// 4. Add new security volumes for each mount
	for _, mount := range mounts {
		glog.Infof("Injecting security volume for secret '%s' at path '%s' (using secret key '%s')",
			mount.secretName, mount.saFilePath, mount.secretKey)
		injectPtpSecurityVolume(daemonSet, mount.secretName, mount.saFilePath, mount.secretHash, mount.secretKey)
	}

	// 5. Convert to Unstructured and apply with merge (like PtpOperatorConfig does)
	scheme := kscheme.Scheme
	updated := &uns.Unstructured{}
	if err := scheme.Convert(daemonSet, updated, nil); err != nil {
		return fmt.Errorf("failed to convert DaemonSet to Unstructured: %v", err)
	}

	// 6. Use apply.ApplyObject which will call MergeObjectForUpdate
	// Set context to indicate this update is from PtpConfig controller
	// This allows the merge logic to use security volumes from updated (even if empty)
	ctxWithSource := context.WithValue(ctx, apply.ControllerSourceKey, apply.SourcePtpConfig)
	if err := apply.ApplyObject(ctxWithSource, r.Client, updated); err != nil {
		return fmt.Errorf("failed to apply DaemonSet: %v", err)
	}

	glog.Info("Successfully updated linuxptp-daemon DaemonSet with security mounts")
	return nil
}

// computeSecretHash generates a SHA256 hash of the secret data for change detection
func computeSecretHash(secret *corev1.Secret) string {
	h := sha256.New()

	// Sort keys for deterministic hash
	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Hash each key-value pair
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(":"))
		h.Write(secret.Data[k])
		h.Write([]byte(";"))
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}

// removeSecurityVolumesFromDaemonSet removes all PTP security-related volumes and mounts from DaemonSet
// Security volumes are identified by: ending with "-ptpconfig-sec" suffix
func removeSecurityVolumesFromDaemonSet(ds *appsv1.DaemonSet) {
	// Remove security volumes (ending with "-ptpconfig-sec")
	var filteredVolumes []corev1.Volume
	for _, vol := range ds.Spec.Template.Spec.Volumes {
		// Check if volume ends with "-ptpconfig-sec" (14 characters)
		if len(vol.Name) >= 14 && vol.Name[len(vol.Name)-14:] == "-ptpconfig-sec" {
			glog.Infof("Removing old security volume: %s", vol.Name)
			continue
		}
		filteredVolumes = append(filteredVolumes, vol)
	}
	ds.Spec.Template.Spec.Volumes = filteredVolumes

	// Remove security volume mounts from linuxptp-daemon-container
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == "linuxptp-daemon-container" {
			var filteredMounts []corev1.VolumeMount
			for _, mount := range ds.Spec.Template.Spec.Containers[i].VolumeMounts {
				// Skip mounts ending with "-ptpconfig-sec" (14 characters)
				if len(mount.Name) >= 14 && mount.Name[len(mount.Name)-14:] == "-ptpconfig-sec" {
					glog.Infof("Removing old security mount: %s", mount.Name)
					continue
				}
				filteredMounts = append(filteredMounts, mount)
			}
			ds.Spec.Template.Spec.Containers[i].VolumeMounts = filteredMounts
			break
		}
	}
}

// lastIndexByte returns the index of the last instance of c in s, or -1 if not present
func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// injectPtpSecurityVolume adds a single secret volume and mount to the DaemonSet
// secretKey is the actual key name in the secret, which gets mapped to the sa_file filename
func injectPtpSecurityVolume(ds *appsv1.DaemonSet, secretName string, saFilePath string, secretHash string, secretKey string) {
	// Use filename from sa_file as volume name
	// Example: /etc/ptp/ptp-security.conf -> ptp-security-ptpconfig-sec
	filename := filepath.Base(saFilePath)

	// Remove extension (everything after the last dot)
	nameWithoutExt := filename
	if lastDot := lastIndexByte(filename, '.'); lastDot >= 0 {
		nameWithoutExt = filename[:lastDot]
	}

	volumeName := nameWithoutExt + "-ptpconfig-sec"

	// 1. Add the volume with items field to map secret key to desired filename
	ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
				Items: []corev1.KeyToPath{
					{
						Key:  secretKey, // The actual key in the secret
						Path: filename,  // Map it to the filename from sa_file
					},
				},
			},
		},
	})

	// 2. Add hash annotation to trigger pod restart on secret change
	if ds.Spec.Template.Annotations == nil {
		ds.Spec.Template.Annotations = make(map[string]string)
	}
	annotationKey := "ptp.openshift.io/secret-hash-" + volumeName
	oldHash := ds.Spec.Template.Annotations[annotationKey]
	if oldHash != secretHash {
		glog.Infof("Secret hash for %s changed (old: %s, new: %s) - pods will be restarted", volumeName, oldHash, secretHash)
		ds.Spec.Template.Annotations[annotationKey] = secretHash
	} else {
		glog.Infof("Secret hash for %s unchanged (%s) - no pod restart needed", volumeName, secretHash)
	}

	// 3. Add volume mount to linuxptp-daemon-container
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == "linuxptp-daemon-container" {
			// Mount using subPath to place the file exactly at saFilePath
			// The items field maps secretKey -> filename, so we use filename as subPath
			ds.Spec.Template.Spec.Containers[i].VolumeMounts = append(
				ds.Spec.Template.Spec.Containers[i].VolumeMounts,
				corev1.VolumeMount{
					Name:      volumeName,
					MountPath: saFilePath,
					ReadOnly:  true,
					SubPath:   filename, // Use the mapped filename from items
				},
			)
			break
		}
	}
}
