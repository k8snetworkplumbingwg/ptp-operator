LOCALBIN ?= $(shell pwd)/../bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)/x86_64/

# Allow PLATFORM to be overridden via environment variable
# If not provided, auto-discover the platform
ifndef PLATFORM
OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
ARCH := $(shell uname -m)

# Normalize architecture names
ifeq ($(ARCH),x86_64)
  ARCH := amd64
endif
ifeq ($(ARCH),aarch64)
  ARCH := arm64
endif

PLATFORM := $(OS)/$(ARCH)
endif

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize

IMG_PREFIX ?= quay.io/<your user id here>/<your image name>
SAVE_DIR ?= /tmp/ptp-images

ifdef GITHUB_ACTIONS
CACHE_FLAG ?=
else
CACHE_FLAG ?= --no-cache
endif

VALUES = lptpd cep ptpop krp prometheus ptpmg debug gnss-sim

.PHONY: print-values
print-values:
	@printf '%s\n' $(VALUES)

podman-buildall: $(foreach v, $(VALUES), podman-build-$(v))

podman-build-%:
	@echo "Platform: $(PLATFORM)"
	$(MAKE) clean-image VAR=$*
	$(MAKE) build-image VAR=$*

podman-pushall: $(foreach v, $(VALUES), podman-push-$(v))

ifdef GITHUB_ACTIONS
podman-push-%:
	podman push ${IMG_PREFIX}:$* docker://${IMG_PREFIX}:$*
else
podman-push-%:
	podman manifest push ${IMG_PREFIX}:$* docker://${IMG_PREFIX}:$*
endif

podman-saveall: $(foreach v, $(VALUES), podman-save-$(v))

ifdef GITHUB_ACTIONS
podman-save-%:
	mkdir -p $(SAVE_DIR)
	podman save -o $(SAVE_DIR)/$*.tar ${IMG_PREFIX}:$*
else
podman-save-%:
	mkdir -p $(SAVE_DIR)
	podman save -m -o $(SAVE_DIR)/$*.tar ${IMG_PREFIX}:$*
endif

podman-build-and-pushall: $(foreach v, $(VALUES), podman-build-and-push-$(v))

podman-build-and-push-%:
	$(MAKE) podman-clean-$*
	$(MAKE) podman-build-$*
	$(MAKE) podman-push-$*

podman-build-and-saveall: $(foreach v, $(VALUES), podman-build-and-save-$(v))

podman-build-and-save-%:
	$(MAKE) podman-clean-$*
	$(MAKE) podman-build-$*
	$(MAKE) podman-save-$*

ifdef GITHUB_ACTIONS
# Parallel podman builds via the remote API can flake with "layer not known"
# while committing COPY layers; retry a few times before failing CI.
build-image:
	@ok=0; \
	for attempt in 1 2 3 4 5; do \
		if podman build $(CACHE_FLAG) --network host --platform $(PLATFORM) $(PODMAN_BUILD_ARGS) -t ${IMG_PREFIX}:$(VAR) -f Dockerfile.$(VAR) ..; then \
			ok=1; break; \
		fi; \
		echo "podman build $(VAR) attempt $${attempt} failed; retrying in $$((attempt * 5))s..."; \
		sleep $$((attempt * 5)); \
	done; \
	if [ "$${ok}" != 1 ]; then exit 1; fi
else
build-image:
	podman manifest create ${IMG_PREFIX}:$(VAR)
	podman build $(CACHE_FLAG) --network host --platform $(PLATFORM) $(PODMAN_BUILD_ARGS) -f Dockerfile.$(VAR) --manifest ${IMG_PREFIX}:$(VAR) ..
endif

ifdef GITHUB_ACTIONS
clean-image:
	podman rmi ${IMG_PREFIX}:$(VAR) || true

podman-clean-%:
	podman rmi ${IMG_PREFIX}:$* || true
else
clean-image:
	podman manifest rm ${IMG_PREFIX}:$(VAR) || true

podman-clean-%:
	podman manifest rm ${IMG_PREFIX}:$* || true
endif

podman-cleanall: $(foreach v, $(VALUES), podman-clean-$(v))

.PHONY: podman-buildall podman-pushall podman-saveall podman-cleanall
.PHONY: podman-build-and-pushall podman-build-and-saveall
.PHONY: build-image clean-image deploy-all

deploy-all: 
	cp ../config/manager/env.yaml ../config/manager/env.yaml.bak
	cp ../config/manager/kustomization.yaml ../config/manager/kustomization.yaml.bak
	./scripts/customize-env.sh ${IMG_PREFIX} ../config/manager
	cd ../config/manager && $(KUSTOMIZE) edit set image controller=${IMG_PREFIX}:ptpop
	$(KUSTOMIZE) build ../config/default | kubectl apply -f - ; \
		mv -f ../config/manager/env.yaml.bak ../config/manager/env.yaml ; \
		mv -f ../config/manager/kustomization.yaml.bak ../config/manager/kustomization.yaml
	$(KUSTOMIZE) build ../config/custom | kubectl apply -f -
	# Wait for the operator pod to be ready so the validating webhook can serve requests.
	kubectl rollout status deployment/ptp-operator -n openshift-ptp --timeout=120s
	@for i in $$(seq 1 30); do \
		if kubectl apply --dry-run=server -n openshift-ptp -f ../config/samples/ptp_v1_ptpoperatorconfig.yaml 2>/dev/null; then \
			break; \
		fi; \
		echo "Waiting for webhook to become ready... ($$i/30)"; \
		sleep 4; \
	done
	kubectl apply -n openshift-ptp -f ../config/samples/ptp_v1_ptpoperatorconfig.yaml
	kubectl patch ptpoperatorconfig default -nopenshift-ptp --type=merge --patch '{"spec": {"ptpEventConfig": {"enableEventPublisher": true, "transportHost": "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043", "storageType": "local-sc"}, "daemonNodeSelector": {"node-role.kubernetes.io/worker": ""}}}'
