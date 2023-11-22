.PHONY: all
all: build-coredns

GO_TEST_FLAGS = $(VERBOSE)

.PHONY: fmt
fmt: ## Run go fmt against code
	go fmt -mod=mod *.go
	git diff --exit-code

.PHONY: vet
vet: ## Run go vet against code
	go vet *.go

.PHONY: test
test: ## Run go test against code
	go test -mod=mod -v $(GO_TEST_FLAGS) ./

.PHONY: build-coredns
build-coredns: ## Build coredns using the local branch of coredns-ocp-dnsnameresolver
	hack/build-coredns.sh

.PHONY: clean
clean:
	go clean
	rm -f coredns

.PHONY: local-image
local-image:
ifndef CONTAINER_IMAGE
	echo "  Please pass a container image ... "
else ifeq ($(CONTAINER_ENGINE), USE_BUILDAH)
	echo "  - Building with buildah ... "
	buildah bud -t $(CONTAINER_IMAGE) .
else ifeq ($(CONTAINER_ENGINE), docker)
	echo "  - Building with docker ... "
	docker build -t $(CONTAINER_IMAGE) .
else ifeq ($(CONTAINER_ENGINE), podman)
	echo "  - Building with podman ... "
	podman build -t $(CONTAINER_IMAGE) .
else
	echo "  Please pass a container engine ... "
endif
