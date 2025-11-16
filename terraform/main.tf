terraform {
  required_version = ">= 1.0"

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.23"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.11"
    }
  }
}

# Kubernetes provider - connects to local k8s (minikube, kind, k3s, etc.)
provider "kubernetes" {
  config_path    = var.kubeconfig_path
  config_context = var.kube_context
}

provider "helm" {
  kubernetes {
    config_path    = var.kubeconfig_path
    config_context = var.kube_context
  }
}

# Create namespace for PostgreSQL clusters
resource "kubernetes_namespace" "postgres" {
  metadata {
    name = var.postgres_namespace
    labels = {
      name       = var.postgres_namespace
      managed-by = "terraform"
      purpose    = "postgres-clusters"
    }
  }
}

# Create namespace for PGAO application
resource "kubernetes_namespace" "pgao" {
  metadata {
    name = var.pgao_namespace
    labels = {
      name       = var.pgao_namespace
      managed-by = "terraform"
      purpose    = "monitoring"
    }
  }
}

# Deploy multiple PostgreSQL clusters to simulate Aurora fleet
module "postgres_cluster_prod_1" {
  source = "./modules/postgres-cluster"

  cluster_name      = "prod-cluster-1"
  namespace         = kubernetes_namespace.postgres.metadata[0].name
  replicas          = 3
  storage_size      = "10Gi"
  postgres_version  = "15"
  postgres_password = var.postgres_password

  resources = {
    requests = {
      cpu    = "500m"
      memory = "1Gi"
    }
    limits = {
      cpu    = "2000m"
      memory = "2Gi"
    }
  }

  labels = {
    environment = "production"
    team        = "platform"
    cluster-id  = "prod-cluster-1"
  }
}

module "postgres_cluster_prod_2" {
  source = "./modules/postgres-cluster"

  cluster_name      = "prod-cluster-2"
  namespace         = kubernetes_namespace.postgres.metadata[0].name
  replicas          = 2
  storage_size      = "10Gi"
  postgres_version  = "15"
  postgres_password = var.postgres_password

  resources = {
    requests = {
      cpu    = "500m"
      memory = "1Gi"
    }
    limits = {
      cpu    = "1000m"
      memory = "1Gi"
    }
  }

  labels = {
    environment = "production"
    team        = "data"
    cluster-id  = "prod-cluster-2"
  }
}

module "postgres_cluster_dev_1" {
  source = "./modules/postgres-cluster"

  cluster_name      = "dev-cluster-1"
  namespace         = kubernetes_namespace.postgres.metadata[0].name
  replicas          = 1
  storage_size      = "5Gi"
  postgres_version  = "14"
  postgres_password = var.postgres_password

  resources = {
    requests = {
      cpu    = "250m"
      memory = "512Mi"
    }
    limits = {
      cpu    = "500m"
      memory = "1Gi"
    }
  }

  labels = {
    environment = "development"
    team        = "platform"
    cluster-id  = "dev-cluster-1"
  }
}

# Deploy PGAO application
resource "kubernetes_config_map" "pgao_config" {
  metadata {
    name      = "pgao-config"
    namespace = kubernetes_namespace.pgao.metadata[0].name
  }

  data = {
    "config.yaml" = templatefile("${path.module}/templates/pgao-config.yaml.tpl", {
      clusters = [
        {
          id       = "prod-cluster-1"
          name     = "Production Cluster 1"
          host     = "${module.postgres_cluster_prod_1.service_name}.${kubernetes_namespace.postgres.metadata[0].name}.svc.cluster.local"
          port     = 5432
          database = "postgres"
        },
        {
          id       = "prod-cluster-2"
          name     = "Production Cluster 2"
          host     = "${module.postgres_cluster_prod_2.service_name}.${kubernetes_namespace.postgres.metadata[0].name}.svc.cluster.local"
          port     = 5432
          database = "postgres"
        },
        {
          id       = "dev-cluster-1"
          name     = "Development Cluster 1"
          host     = "${module.postgres_cluster_dev_1.service_name}.${kubernetes_namespace.postgres.metadata[0].name}.svc.cluster.local"
          port     = 5432
          database = "postgres"
        }
      ]
    })
  }
}

resource "kubernetes_secret" "pgao_secrets" {
  metadata {
    name      = "pgao-secrets"
    namespace = kubernetes_namespace.pgao.metadata[0].name
  }

  data = {
    postgres-password = var.postgres_password
  }

  type = "Opaque"
}

resource "kubernetes_deployment" "pgao" {
  metadata {
    name      = "pgao"
    namespace = kubernetes_namespace.pgao.metadata[0].name
    labels = {
      app     = "pgao"
      version = "v1"
    }
  }

  # Ensure PostgreSQL clusters are deployed first
  depends_on = [
    module.postgres_cluster_prod_1,
    module.postgres_cluster_prod_2,
    module.postgres_cluster_dev_1
  ]

  spec {
    replicas = 2

    selector {
      match_labels = {
        app = "pgao"
      }
    }

    template {
      metadata {
        labels = {
          app     = "pgao"
          version = "v1"
        }
      }

      spec {
        container {
          name              = "pgao"
          image             = var.pgao_image
          image_pull_policy = "Never"

          port {
            name           = "http"
            container_port = 8080
            protocol       = "TCP"
          }

          env {
            name  = "LOG_LEVEL"
            value = "info"
          }

          env {
            name  = "CONFIG_PATH"
            value = "/etc/pgao/config.yaml"
          }

          env {
            name = "DATABASE_PASSWORD"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.pgao_secrets.metadata[0].name
                key  = "postgres-password"
              }
            }
          }

          volume_mount {
            name       = "config"
            mount_path = "/etc/pgao"
            read_only  = true
          }

          liveness_probe {
            http_get {
              path = "/health"
              port = 8080
            }
            initial_delay_seconds = 30
            period_seconds        = 10
          }

          startup_probe {
            http_get {
              path = "/health"
              port = 8080
            }
            initial_delay_seconds = 10
            period_seconds        = 5
            failure_threshold     = 30 # Allow up to 150 seconds (30 * 5s) for startup
          }

          readiness_probe {
            http_get {
              path = "/ready"
              port = 8080
            }
            initial_delay_seconds = 30
            period_seconds        = 10
            timeout_seconds       = 5
            failure_threshold     = 3
          }

          resources {
            requests = {
              cpu    = "250m"
              memory = "256Mi"
            }
            limits = {
              cpu    = "500m"
              memory = "512Mi"
            }
          }
        }

        volume {
          name = "config"
          config_map {
            name = kubernetes_config_map.pgao_config.metadata[0].name
          }
        }
      }
    }
  }
}

resource "kubernetes_service" "pgao" {
  metadata {
    name      = "pgao"
    namespace = kubernetes_namespace.pgao.metadata[0].name
    labels = {
      app = "pgao"
    }
  }

  spec {
    selector = {
      app = "pgao"
    }

    port {
      name        = "http"
      port        = 8080
      target_port = 8080
      protocol    = "TCP"
    }

    type = "NodePort"
  }
}
