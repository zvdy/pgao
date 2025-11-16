# Makefile for PostgreSQL Analytics Observer

# Variables
BINARY_NAME=pgao
DOCKER_IMAGE=pgao
DOCKER_TAG=latest
GO_FILES=$(shell find src -name '*.go')
MAIN_PATH=src/main.go

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Build flags
LDFLAGS=-ldflags "-w -s"

.PHONY: all build clean test coverage fmt lint deps run docker-build docker-run docker-push \
	terraform-init terraform-plan terraform-apply terraform-destroy terraform-fmt terraform-lint \
	k8s-deploy k8s-delete k8s-status help

# Default target
all: clean deps fmt build test

# Build the application
build:
	@echo "Building $(BINARY_NAME)..."
	$(GOBUILD) $(LDFLAGS) -o bin/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: bin/$(BINARY_NAME)"

# Build for multiple platforms
build-all:
	@echo "Building for multiple platforms..."
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o bin/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "Multi-platform build complete"

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...

# Generate test coverage report
coverage: test
	@echo "Generating coverage report..."
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) ./...

# Lint code
lint:
	@echo "Linting code..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

# Install/update dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -rf bin/
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# Run the application
run: build
	@echo "Running $(BINARY_NAME)..."
	./bin/$(BINARY_NAME)

# Run with config file
run-with-config: build
	@echo "Running $(BINARY_NAME) with config..."
	CONFIG_PATH=config.yaml ./bin/$(BINARY_NAME)

# Docker commands
docker-build:
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	@echo "Docker image built: $(DOCKER_IMAGE):$(DOCKER_TAG)"

docker-run:
	@echo "Running Docker container..."
	docker run -d \
		--name $(BINARY_NAME) \
		-p 8080:8080 \
		-e LOG_LEVEL=info \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

docker-stop:
	@echo "Stopping Docker container..."
	docker stop $(BINARY_NAME) || true
	docker rm $(BINARY_NAME) || true

docker-push:
	@echo "Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

docker-logs:
	docker logs -f $(BINARY_NAME)

# Terraform commands
terraform-init:
	@echo "Initializing Terraform..."
	cd terraform && terraform init

terraform-plan:
	@echo "Planning Terraform changes..."
	cd terraform && terraform plan

terraform-apply:
	@echo "Applying Terraform changes..."
	cd terraform && terraform apply

terraform-destroy:
	@echo "Destroying Terraform infrastructure..."
	cd terraform && terraform destroy

terraform-fmt:
	@echo "Formatting Terraform files..."
	terraform fmt -recursive terraform/

terraform-lint:
	@echo "Linting Terraform files..."
	cd terraform && terraform validate
	@if command -v tflint >/dev/null 2>&1; then \
		echo "Running tflint..."; \
		cd terraform && tflint; \
	else \
		echo "tflint not installed, skipping (install with: curl -s https://raw.githubusercontent.com/terraform-linters/tflint/master/install_linux.sh | bash)"; \
	fi

# Kubernetes commands
k8s-deploy:
	@echo "Deploying to Kubernetes..."
	kubectl apply -f kubernetes/configmaps/
	kubectl apply -f kubernetes/deployments/
	kubectl apply -f kubernetes/services/

k8s-delete:
	@echo "Deleting from Kubernetes..."
	kubectl delete -f kubernetes/services/ || true
	kubectl delete -f kubernetes/deployments/ || true
	kubectl delete -f kubernetes/configmaps/ || true

k8s-status:
	@echo "Checking Kubernetes status..."
	kubectl get pods,services,deployments -l app=pgao

k8s-logs:
	@echo "Fetching logs..."
	kubectl logs -l app=pgao -f

k8s-restart:
	@echo "Restarting deployment..."
	kubectl rollout restart deployment/pgao

# Database commands
db-migrate:
	@echo "Running database migrations..."
	# Add migration commands here

db-seed:
	@echo "Seeding database..."
	# Add seed commands here

# Development helpers
dev-setup:
	@echo "Setting up development environment..."
	$(MAKE) deps
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "Development setup complete"

watch:
	@echo "Watching for changes..."
	@if command -v air >/dev/null 2>&1; then \
		air; \
	else \
		echo "air not installed. Install with: go install github.com/cosmtrek/air@latest"; \
	fi

# Generate code
generate:
	@echo "Generating code..."
	$(GOCMD) generate ./...

# Security scan
security-scan:
	@echo "Running security scan..."
	@if command -v gosec >/dev/null 2>&1; then \
		gosec ./...; \
	else \
		echo "gosec not installed. Install with: go install github.com/securego/gosec/v2/cmd/gosec@latest"; \
	fi

# Benchmarks
bench:
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./...

# Help command
help:
	@echo "PostgreSQL Analytics Observer - Makefile commands:"
	@echo ""
	@echo "Build Commands:"
	@echo "  make build          - Build the application"
	@echo "  make build-all      - Build for multiple platforms"
	@echo "  make clean          - Clean build artifacts"
	@echo ""
	@echo "Development Commands:"
	@echo "  make run            - Build and run the application"
	@echo "  make test           - Run tests"
	@echo "  make coverage       - Generate test coverage report"
	@echo "  make fmt            - Format code"
	@echo "  make lint           - Lint code"
	@echo "  make watch          - Watch for changes and rebuild"
	@echo "  make dev-setup      - Setup development environment"
	@echo ""
	@echo "Docker Commands:"
	@echo "  make docker-build   - Build Docker image"
	@echo "  make docker-run     - Run Docker container"
	@echo "  make docker-stop    - Stop Docker container"
	@echo "  make docker-push    - Push Docker image"
	@echo ""
	@echo "Terraform Commands:"
	@echo "  make terraform-init     - Initialize Terraform"
	@echo "  make terraform-plan     - Plan Terraform changes"
	@echo "  make terraform-apply    - Apply Terraform changes"
	@echo "  make terraform-destroy  - Destroy infrastructure"
	@echo ""
	@echo "Kubernetes Commands:"
	@echo "  make k8s-deploy     - Deploy to Kubernetes"
	@echo "  make k8s-delete     - Delete from Kubernetes"
	@echo "  make k8s-status     - Check deployment status"
	@echo "  make k8s-logs       - View logs"
	@echo "  make k8s-restart    - Restart deployment"
	@echo ""
	@echo "Other Commands:"
	@echo "  make deps           - Download/update dependencies"
	@echo "  make security-scan  - Run security scan"
	@echo "  make bench          - Run benchmarks"
	@echo "  make help           - Show this help message"
