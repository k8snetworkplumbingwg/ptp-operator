package controllers

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// stubClient implements client.Client for testing. Only Get is used by
// SecurityProfileWatcher.Reconcile; all other methods panic.
type stubClient struct {
	client.Client // embed to satisfy the interface
	apiServer     *configv1.APIServer
}

func (s *stubClient) Get(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	src := s.apiServer.DeepCopy()
	dst := obj.(*configv1.APIServer)
	*dst = *src
	return nil
}

func (s *stubClient) Scheme() *runtime.Scheme {
	sc := runtime.NewScheme()
	_ = configv1.Install(sc)
	return sc
}

func newFakeAPIServer(profileType configv1.TLSProfileType, adherence configv1.TLSAdherencePolicy) *configv1.APIServer {
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSAdherence: adherence,
		},
	}
	if profileType != "" {
		apiServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
			Type: profileType,
		}
	}
	return apiServer
}

func TestSecurityProfileWatcher_ProfileChangeTriggersCancelation(t *testing.T) {
	initialProfile, _ := openshifttls.GetTLSProfileSpec(nil) // Intermediate
	apiServer := newFakeAPIServer(configv1.TLSProfileModernType, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var profileChanged bool
	watcher := &openshifttls.SecurityProfileWatcher{
		Client:                &stubClient{apiServer: apiServer},
		InitialTLSProfileSpec: initialProfile,
		OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
			profileChanged = true
			cancel()
		},
	}

	_, err := watcher.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	})
	assert.NoError(t, err)
	assert.True(t, profileChanged, "OnProfileChange should have been called")
	assert.Error(t, ctx.Err(), "context should be cancelled after profile change")
}

func TestSecurityProfileWatcher_AdherencePolicyChangeTriggersCancelation(t *testing.T) {
	initialProfile, _ := openshifttls.GetTLSProfileSpec(nil) // Intermediate
	apiServer := newFakeAPIServer("", configv1.TLSAdherencePolicyStrictAllComponents)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var adherenceChanged bool
	watcher := &openshifttls.SecurityProfileWatcher{
		Client:                    &stubClient{apiServer: apiServer},
		InitialTLSProfileSpec:     initialProfile,
		InitialTLSAdherencePolicy: configv1.TLSAdherencePolicyNoOpinion,
		OnAdherencePolicyChange: func(_ context.Context, _, _ configv1.TLSAdherencePolicy) {
			adherenceChanged = true
			cancel()
		},
	}

	_, err := watcher.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	})
	assert.NoError(t, err)
	assert.True(t, adherenceChanged, "OnAdherencePolicyChange should have been called")
	assert.Error(t, ctx.Err(), "context should be cancelled after adherence change")
}

func TestSecurityProfileWatcher_NoChangeDoesNotTrigger(t *testing.T) {
	initialProfile, _ := openshifttls.GetTLSProfileSpec(nil) // Intermediate
	apiServer := newFakeAPIServer("", configv1.TLSAdherencePolicyNoOpinion)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var triggered bool
	watcher := &openshifttls.SecurityProfileWatcher{
		Client:                    &stubClient{apiServer: apiServer},
		InitialTLSProfileSpec:     initialProfile,
		InitialTLSAdherencePolicy: configv1.TLSAdherencePolicyNoOpinion,
		OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
			triggered = true
		},
		OnAdherencePolicyChange: func(_ context.Context, _, _ configv1.TLSAdherencePolicy) {
			triggered = true
		},
	}

	_, err := watcher.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	})
	assert.NoError(t, err)
	assert.False(t, triggered, "callbacks should not be called when nothing changed")
	assert.NoError(t, ctx.Err(), "context should not be cancelled")
}

func TestSecurityProfileWatcher_BothChangeTriggerBothCallbacks(t *testing.T) {
	initialProfile, _ := openshifttls.GetTLSProfileSpec(nil) // Intermediate
	apiServer := newFakeAPIServer(configv1.TLSProfileOldType, configv1.TLSAdherencePolicyStrictAllComponents)

	var profileChanged, adherenceChanged bool
	watcher := &openshifttls.SecurityProfileWatcher{
		Client:                    &stubClient{apiServer: apiServer},
		InitialTLSProfileSpec:     initialProfile,
		InitialTLSAdherencePolicy: configv1.TLSAdherencePolicyNoOpinion,
		OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
			profileChanged = true
		},
		OnAdherencePolicyChange: func(_ context.Context, _, _ configv1.TLSAdherencePolicy) {
			adherenceChanged = true
		},
	}

	_, err := watcher.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster"},
	})
	assert.NoError(t, err)
	assert.True(t, profileChanged, "OnProfileChange should have been called")
	assert.True(t, adherenceChanged, "OnAdherencePolicyChange should have been called")
}
