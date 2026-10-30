module github.com/k8snetworkplumbingwg/ptp-operator/test

go 1.26.0

replace (
	github.com/k8snetworkplumbingwg/ptp-operator => ..

	// Must match root go.mod: openshift/client-go v0.0.1 uses structured-merge-diff/v4,
	// incompatible with v6 required by the parent module's k8s 0.34.x deps.
	github.com/openshift/api => github.com/openshift/api v0.0.0-20260107103503-6d35063ca179
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20260108185524-48f4ccfc4e13
)

// Placeholder for dependencies - will be populated by go mod tidy
require (
	github.com/k8snetworkplumbingwg/ptp-operator v0.0.0-20260318181654-4ec2565ae595
	github.com/onsi/ginkgo/v2 v2.28.1
	github.com/onsi/gomega v1.39.1
)

require (
	github.com/Masterminds/semver v1.5.0
	github.com/Masterminds/semver/v3 v3.4.0
	github.com/cloudevents/sdk-go/v2 v2.15.2
	github.com/creasty/defaults v1.6.0
	github.com/facebook/time v0.0.0-20241030181404-3e1b98825c29
	github.com/golang/glog v1.2.5
	github.com/openshift/api v0.0.1
	github.com/openshift/client-go v0.0.1
	github.com/openshift/library-go v0.0.0-20260807194649-ee0a87843dda
	github.com/pkg/errors v0.9.1
	github.com/prometheus-operator/prometheus-operator/pkg/client v0.57.0
	github.com/prometheus/common v0.67.5
	github.com/redhat-cne/channel-pubsub v0.0.8
	github.com/redhat-cne/l2discovery-lib v0.1.4
	github.com/redhat-cne/privileged-daemonset v1.0.35
	github.com/redhat-cne/ptp-listener-exports v0.0.7
	github.com/redhat-cne/sdk-go v1.22.4
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.36.2
	k8s.io/apiextensions-apiserver v0.36.2
	k8s.io/apimachinery v0.36.2
	k8s.io/client-go v0.36.2
	k8s.io/utils v0.0.0-20260707023825-cf1189d6abe3
	sigs.k8s.io/controller-runtime v0.24.1
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.25.4 // indirect
	github.com/go-openapi/swag/cmdutils v0.25.4 // indirect
	github.com/go-openapi/swag/conv v0.25.4 // indirect
	github.com/go-openapi/swag/fileutils v0.25.4 // indirect
	github.com/go-openapi/swag/jsonname v0.25.4 // indirect
	github.com/go-openapi/swag/jsonutils v0.25.4 // indirect
	github.com/go-openapi/swag/loading v0.25.4 // indirect
	github.com/go-openapi/swag/mangling v0.25.4 // indirect
	github.com/go-openapi/swag/netutils v0.25.4 // indirect
	github.com/go-openapi/swag/stringutils v0.25.4 // indirect
	github.com/go-openapi/swag/typeutils v0.25.4 // indirect
	github.com/go-openapi/swag/yamlutils v0.25.4 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/pprof v0.0.0-20260115054156-294ebfa9ad83 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/moby/spdystream v0.5.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.74.0 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/procfs v0.19.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/yourbasic/graph v0.0.0-20210606180040-8ecfec1c2869 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	go.yaml.in/yaml/v2 v2.4.3 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260519202549-bbf5c5577288 // indirect
	k8s.io/streaming v0.36.2 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.2 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
