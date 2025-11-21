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
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

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
		Complete(r)
}

// secretMount represents a secret and sa_file pair for a profile
type secretMount struct {
	secretName string
	saFilePath string
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

			// Load secret to get the key name
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

			mounts = append(mounts, secretMount{
				secretName: secretName,
				saFilePath: saFilePath,
				secretKey:  secretKey,
			})
		}
	}

	// Group by directory - if multiple secrets target same directory, combine them using projected volume
	type directoryGroup struct {
		mountDir string
		secrets  []secretMount
	}

	dirGroups := make(map[string]*directoryGroup)
	for _, mount := range mounts {
		mountDir := filepath.Dir(mount.saFilePath)
		if _, exists := dirGroups[mountDir]; !exists {
			dirGroups[mountDir] = &directoryGroup{
				mountDir: mountDir,
				secrets:  []secretMount{},
			}
		}
		dirGroups[mountDir].secrets = append(dirGroups[mountDir].secrets, mount)
	}

	// Deduplicate secrets within each directory group
	// Multiple PtpConfigs may reference the same secret+file combination
	for mountDir, group := range dirGroups {
		uniqueSecrets := make(map[string]secretMount) // Key: secretName+saFilePath
		for _, mount := range group.secrets {
			key := mount.secretName + ":" + mount.saFilePath
			if _, exists := uniqueSecrets[key]; !exists {
				uniqueSecrets[key] = mount
			}
		}
		// Replace with deduplicated list
		group.secrets = []secretMount{}
		for _, mount := range uniqueSecrets {
			group.secrets = append(group.secrets, mount)
		}
		glog.Infof("Directory '%s': %d unique secrets after deduplication", mountDir, len(group.secrets))
	}

	glog.Infof("Found %d directory(ies) with unique secret(s)", len(dirGroups))

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

	// 4. Add volumes - use projected volume if multiple secrets share a directory
	for _, group := range dirGroups {
		if len(group.secrets) == 1 {
			// Single secret in this directory - use simple secret volume
			mount := group.secrets[0]
			glog.Infof("Injecting security volume for secret '%s' at path '%s' (using secret key '%s')",
				mount.secretName, mount.saFilePath, mount.secretKey)
			injectPtpSecurityVolume(daemonSet, mount.secretName, mount.saFilePath, mount.secretKey)
		} else {
			// Multiple secrets in same directory - use projected volume to combine them
			glog.Infof("Injecting projected volume for directory '%s' with %d secrets", group.mountDir, len(group.secrets))
			injectPtpSecurityProjectedVolume(daemonSet, group.mountDir, group.secrets)
		}
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

// removeSecurityVolumesFromDaemonSet removes all PTP security-related volumes and mounts from DaemonSet
// Security volumes are identified by: ending with "-ptpconfig-sec" suffix OR starting with "ptp-secrets-" prefix
func removeSecurityVolumesFromDaemonSet(ds *appsv1.DaemonSet) {
	// Remove security volumes (both regular and projected)
	var filteredVolumes []corev1.Volume
	for _, vol := range ds.Spec.Template.Spec.Volumes {
		isSecurityVolume := false
		// Check if volume ends with "-ptpconfig-sec" (14 characters)
		if len(vol.Name) >= 14 && vol.Name[len(vol.Name)-14:] == "-ptpconfig-sec" {
			isSecurityVolume = true
		}
		// Check if volume starts with "ptp-secrets-" (projected volumes)
		if strings.HasPrefix(vol.Name, "ptp-secrets-") {
			isSecurityVolume = true
		}

		if isSecurityVolume {
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
				isSecurityMount := false
				// Skip mounts ending with "-ptpconfig-sec" (14 characters)
				if len(mount.Name) >= 14 && mount.Name[len(mount.Name)-14:] == "-ptpconfig-sec" {
					isSecurityMount = true
				}
				// Skip mounts starting with "ptp-secrets-" (projected volumes)
				if strings.HasPrefix(mount.Name, "ptp-secrets-") {
					isSecurityMount = true
				}

				if isSecurityMount {
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

// injectPtpSecurityProjectedVolume creates a projected volume combining multiple secrets for the same directory
// This is needed because Kubernetes requires unique mountPath - we can't have multiple mounts to same directory
func injectPtpSecurityProjectedVolume(ds *appsv1.DaemonSet, mountDir string, secrets []secretMount) {
	// Create volume name from directory path
	volumeName := "ptp-secrets-" + strings.ReplaceAll(strings.Trim(mountDir, "/"), "/", "-")

	// Build projected sources from all secrets
	var sources []corev1.VolumeProjection
	for _, mount := range secrets {
		filename := filepath.Base(mount.saFilePath)
		sources = append(sources, corev1.VolumeProjection{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: mount.secretName,
				},
				Items: []corev1.KeyToPath{
					{
						Key:  mount.secretKey,
						Path: filename,
					},
				},
			},
		})
		glog.Infof("  - Adding secret '%s' key '%s' as file '%s'", mount.secretName, mount.secretKey, filename)
	}

	// Add projected volume
	ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources: sources,
			},
		},
	})

	// Add ONE volume mount for all secrets in this directory
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == "linuxptp-daemon-container" {
			ds.Spec.Template.Spec.Containers[i].VolumeMounts = append(
				ds.Spec.Template.Spec.Containers[i].VolumeMounts,
				corev1.VolumeMount{
					Name:      volumeName,
					MountPath: mountDir,
					ReadOnly:  true,
				},
			)
			break
		}
	}
}

// injectPtpSecurityVolume adds a single secret volume and mount to the DaemonSet
// secretKey is the actual key name in the secret, which gets mapped to the sa_file filename
func injectPtpSecurityVolume(ds *appsv1.DaemonSet, secretName string, saFilePath string, secretKey string) {
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

	// 2. Add volume mount to linuxptp-daemon-container
	// Mount to directory WITHOUT subPath to enable Kubernetes auto-updates when secret changes
	// File will appear at: <directory>/<filename> (exactly as user specified in sa_file)
	mountDir := filepath.Dir(saFilePath)

	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == "linuxptp-daemon-container" {
			// Mount to directory (not file) WITHOUT subPath
			// This allows Kubernetes to auto-update file content when secret changes
			ds.Spec.Template.Spec.Containers[i].VolumeMounts = append(
				ds.Spec.Template.Spec.Containers[i].VolumeMounts,
				corev1.VolumeMount{
					Name:      volumeName,
					MountPath: mountDir, // User's directory from sa_file path
					ReadOnly:  true,
					// NO subPath - enables automatic file updates!
				},
			)
			break
		}
	}

	glog.Infof("Mounted secret '%s' to directory %s, file will be at %s",
		secretName, mountDir, saFilePath)
}
