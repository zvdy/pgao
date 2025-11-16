# Kubernetes Configuration
variable "kubeconfig_path" {
  description = "Path to kubeconfig file"
  type        = string
  default     = "~/.kube/config"
}

variable "kube_context" {
  description = "Kubernetes context to use"
  type        = string
  default     = "minikube"
}

# Namespace Configuration
variable "postgres_namespace" {
  description = "Namespace for PostgreSQL clusters"
  type        = string
  default     = "postgres-clusters"
}

variable "pgao_namespace" {
  description = "Namespace for PGAO application"
  type        = string
  default     = "pgao"
}

# PostgreSQL Configuration
variable "postgres_password" {
  description = "Password for PostgreSQL admin user"
  type        = string
  sensitive   = true
  default     = "changeme123"
}

# PGAO Application Configuration
variable "pgao_image" {
  description = "Docker image for PGAO application"
  type        = string
  default     = "pgao:latest"
}
