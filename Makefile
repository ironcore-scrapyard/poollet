
# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.24.1

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./testdata/api/..." crd output:crd:artifacts:config=testdata/config/crd

.PHONY: addlicense
addlicense: ## Add license headers to all go files.
	find . -name '*.go' -exec go run github.com/google/addlicense -c 'OnMetal authors' {} +

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: checklicense
checklicense: ## Check that every file has a license header present.
	find . -name '*.go' -exec go run github.com/google/addlicense  -check -c 'OnMetal authors' {} +

.PHONY: lint
lint: ## Run golangci-lint against code.
	golangci-lint run ./...

.PHONY: check
check: generate addlicense lint test

ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
.PHONY: test
test: envtest generate fmt checklicense ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" go test ./... -coverprofile cover.out

.PHONY: check
check: generate fmt addlicense lint test ## Lint and run tests.

##@ Build

.PHONY: build-base
build-base: generate fmt addlicense lint ## Basic build steps

.PHONY: build-partitionlet
build-partitionlet: build-base ## Build partitionlet
	go build -o bin/partitionlet ./partitionlet/main.go

.PHONY: build-machinebrokerlet
build-machinebrokerlet: build-base ## Build machinebrokerlet
	go build -o bin/machinebrokerlet ./machinebrokerlet/main.go

.PHONY: build-volumebrokerlet
build-volumebrokerlet: build-base ## Build volumebrokerlet
	go build -o bin/volumebrokerlet ./volumebrokerlet/main.go

.PHONY: build-proxyvolumebrokerlet
build-proxyvolumebrokerlet: build-base ## Build proxyvolumebrokerlet
	go build -o bin/proxyvolumebrokerlet ./proxyvolumebrokerlet/main.go

.PHONY: build
build: build-partitionlet build-machinebrokerlet build-volumebrokerlet build-volumebrokerlet ## Build all binaries

.PHONY: run-base
run-base: generate fmt lint ## Basic steps before running anything

.PHONY: run-partitionlet
run-partitionlet: run-base ## Run partitionlet
	go run ./partitionlet/main.go

.PHONY: run-machinebrokerlet
run-machinebrokerlet: run-base ## Run machinebrokerlet
	go run ./machinebrokerlet/main.go

.PHONY: run-volumebrokerlet
run-volumebrokerlet: run-base ## Run volumebrokerlet
	go run ./volumebrokerlet/main.go

.PHONY: run-proxyvolumebrokerlet
run-proxyvolumebrokerlet: run-base ## Run proxyvolumebrokerlet
	go run ./proxyvolumebrokerlet/main.go

.PHONY: docker-build-partitionlet
docker-build-partitionlet: test ## Build docker image with partitionlet.
	docker build --target partitionlet -t ${IMG} .

.PHONY: docker-build-machinebrokerlet
docker-build-machinebrokerlet: test ## Build docker image with machinebrokerlet.
	docker build --target machinebrokerlet -t ${IMG} .

.PHONY: docker-build-volumebrokerlet
docker-build-volumebrokerlet: test ## Build docker image with volumebrokerlet.
	docker build --target volumebrokerlet -t ${IMG} .

.PHONY: docker-build-proxyvolumebrokerlet
docker-build-proxyvolumebrokerlet: test ## Build docker image with proxyvolumebrokerlet.
	docker build --target proxyvolumebrokerlet -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

.PHONY: deploy-partitionlet
deploy-partitionlet: kustomize ## Deploy partitionlet into the K8s cluster specified in ~/.kube/config.
	cd config/partitionlet/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	kubectl apply -k config/partitionlet/default

.PHONY: deploy-machinebrokerlet
deploy-machinebrokerlet: kustomize ## Deploy machinebrokerlet into the K8s cluster specified in ~/.kube/config.
	cd config/machinebrokerlet/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	kubectl apply -k config/machinebrokerlet/default

.PHONY: deploy-volumebrokerlet
deploy-volumebrokerlet: kustomize ## Deploy volumebrokerlet into the K8s cluster specified in ~/.kube/config.
	cd config/volumebrokerlet/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	kubectl apply -k config/volumebrokerlet/default

.PHONY: deploy-proxyvolumebrokerlet
deploy-proxyvolumebrokerlet: kustomize ## Deploy proxyvolumebrokerlet into the K8s cluster specified in ~/.kube/config.
	cd config/proxyvolumebrokerlet/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	kubectl apply -k config/proxyvolumebrokerlet/default

.PHONY: undeploy-partitionlet
undeploy-partitionlet: kustomize ## Undeploy partitionlet from the K8s cluster specified in ~/.kube/config.
	kubectl delete -k config/partitionlet/default

.PHONY: undeploy-machinebrokerlet
undeploy-machinebrokerlet: kustomize ## Undeploy machinebrokerlet from the K8s cluster specified in ~/.kube/config.
	kubectl delete -k config/machinebrokerlet/default

.PHONY: undeploy-volumebrokerlet
undeploy-volumebrokerlet: kustomize ## Undeploy volumebrokerlet from the K8s cluster specified in ~/.kube/config.
	kubectl delete -k config/volumebrokerlet/default

.PHONY: undeploy-proxyvolumebrokerlet
undeploy-proxyvolumebrokerlet: kustomize ## Undeploy proxyvolumebrokerlet from the K8s cluster specified in ~/.kube/config.
	kubectl delete -k config/proxyvolumebrokerlet/default

##@ Tools

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest

## Tool Versions
KUSTOMIZE_VERSION ?= v3.8.7
CONTROLLER_TOOLS_VERSION ?= v0.9.0

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	curl -s $(KUSTOMIZE_INSTALL_SCRIPT) | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
