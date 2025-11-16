variable "cluster_name" {
  description = "Name of the PostgreSQL cluster"
  type        = string
}

variable "namespace" {
  description = "Kubernetes namespace"
  type        = string
}

variable "replicas" {
  description = "Number of PostgreSQL replicas"
  type        = number
  default     = 1
}

variable "storage_size" {
  description = "Storage size for each replica"
  type        = string
  default     = "10Gi"
}

variable "postgres_version" {
  description = "PostgreSQL version"
  type        = string
  default     = "15"
}

variable "postgres_password" {
  description = "PostgreSQL password"
  type        = string
  sensitive   = true
}

variable "resources" {
  description = "Resource requests and limits"
  type = object({
    requests = map(string)
    limits   = map(string)
  })
  default = {
    requests = {
      cpu    = "250m"
      memory = "512Mi"
    }
    limits = {
      cpu    = "1000m"
      memory = "1Gi"
    }
  }
}

variable "labels" {
  description = "Additional labels for the cluster"
  type        = map(string)
  default     = {}
}
