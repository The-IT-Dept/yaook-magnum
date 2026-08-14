IMG ?= ghcr.io/the-it-dept/yaook-magnum:latest
CONTROLLER_GEN ?= $(shell go env GOPATH)/bin/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.19.0

.PHONY: all
all: build

.PHONY: controller-gen
controller-gen:
	@test -x $(CONTROLLER_GEN) || go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: generate
generate: controller-gen ## Generate deepcopy, CRD and RBAC manifests.
	$(CONTROLLER_GEN) object paths=./api/...
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=yaook-magnum-manager paths=./internal/... output:rbac:artifacts:config=config/rbac

.PHONY: fmt vet
fmt:
	go fmt ./...
vet:
	go vet ./...

.PHONY: test
test: fmt vet
	go test ./... -count=1

.PHONY: build
build: fmt vet
	go build -o bin/manager cmd/main.go

.PHONY: docker-build
docker-build:
	docker build --platform linux/amd64 -t $(IMG) .

.PHONY: docker-push
docker-push:
	docker push $(IMG)

.PHONY: install
install: ## Install the CRD into the current cluster.
	kubectl apply -f config/crd/bases/

.PHONY: deploy
deploy: install ## Deploy the operator into the current cluster.
	kubectl apply -f deploy/operator.yaml

.PHONY: undeploy
undeploy:
	kubectl delete -f deploy/operator.yaml --ignore-not-found
