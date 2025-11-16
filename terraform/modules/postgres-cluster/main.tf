# Create a Secret for PostgreSQL password
resource "kubernetes_secret" "postgres_password" {
  metadata {
    name      = "${var.cluster_name}-password"
    namespace = var.namespace
  }

  data = {
    password = var.postgres_password
  }

  type = "Opaque"
}

# Create a ConfigMap for PostgreSQL configuration
resource "kubernetes_config_map" "postgres_config" {
  metadata {
    name      = "${var.cluster_name}-config"
    namespace = var.namespace
  }

  data = {
    "postgresql.conf" = <<-EOF
      # PostgreSQL Configuration
      max_connections = 200
      shared_buffers = 256MB
      effective_cache_size = 1GB
      maintenance_work_mem = 64MB
      work_mem = 4MB
      
      # Write Ahead Log
      wal_level = replica
      max_wal_senders = 10
      max_replication_slots = 10
      
      # Logging
      logging_collector = on
      log_destination = 'stderr'
      log_statement = 'all'
      log_duration = on
      log_line_prefix = '%t [%p]: [%l-1] user=%u,db=%d,app=%a,client=%h '
      
      # Performance
      random_page_cost = 1.1
      effective_io_concurrency = 200
      
      # Extensions
      shared_preload_libraries = 'pg_stat_statements'
      pg_stat_statements.track = all
    EOF
  }
}

# Create StatefulSet for PostgreSQL
resource "kubernetes_stateful_set" "postgres" {
  metadata {
    name      = var.cluster_name
    namespace = var.namespace
    labels = merge(
      {
        app     = "postgres"
        cluster = var.cluster_name
      },
      var.labels
    )
  }

  spec {
    service_name = var.cluster_name
    replicas     = var.replicas

    selector {
      match_labels = {
        app     = "postgres"
        cluster = var.cluster_name
      }
    }

    template {
      metadata {
        labels = merge(
          {
            app     = "postgres"
            cluster = var.cluster_name
          },
          var.labels
        )
      }

      spec {
        container {
          name  = "postgres"
          image = "postgres:${var.postgres_version}-alpine"

          port {
            name           = "postgres"
            container_port = 5432
            protocol       = "TCP"
          }

          env {
            name = "POSTGRES_PASSWORD"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.postgres_password.metadata[0].name
                key  = "password"
              }
            }
          }

          env {
            name  = "POSTGRES_USER"
            value = "postgres"
          }

          env {
            name  = "POSTGRES_DB"
            value = "postgres"
          }

          env {
            name  = "PGDATA"
            value = "/var/lib/postgresql/data/pgdata"
          }

          volume_mount {
            name       = "data"
            mount_path = "/var/lib/postgresql/data"
          }

          volume_mount {
            name       = "config"
            mount_path = "/etc/postgresql"
          }

          liveness_probe {
            exec {
              command = ["pg_isready", "-U", "postgres"]
            }
            initial_delay_seconds = 30
            period_seconds        = 10
            timeout_seconds       = 5
            failure_threshold     = 3
          }

          readiness_probe {
            exec {
              command = ["pg_isready", "-U", "postgres"]
            }
            initial_delay_seconds = 10
            period_seconds        = 5
            timeout_seconds       = 3
            failure_threshold     = 3
          }

          resources {
            requests = var.resources.requests
            limits   = var.resources.limits
          }
        }

        volume {
          name = "config"
          config_map {
            name = kubernetes_config_map.postgres_config.metadata[0].name
          }
        }

        init_container {
          name  = "init-postgres"
          image = "postgres:${var.postgres_version}-alpine"

          command = [
            "sh",
            "-c",
            <<-EOF
              echo "Initializing PostgreSQL..."
              chown -R 70:70 /var/lib/postgresql/data || true
            EOF
          ]

          volume_mount {
            name       = "data"
            mount_path = "/var/lib/postgresql/data"
          }

          security_context {
            run_as_user = 0
          }
        }
      }
    }

    volume_claim_template {
      metadata {
        name = "data"
      }

      spec {
        access_modes = ["ReadWriteOnce"]

        resources {
          requests = {
            storage = var.storage_size
          }
        }
      }
    }
  }
}

# Create a Service for PostgreSQL
resource "kubernetes_service" "postgres" {
  metadata {
    name      = var.cluster_name
    namespace = var.namespace
    labels = merge(
      {
        app     = "postgres"
        cluster = var.cluster_name
      },
      var.labels
    )
  }

  spec {
    selector = {
      app     = "postgres"
      cluster = var.cluster_name
    }

    port {
      name        = "postgres"
      port        = 5432
      target_port = 5432
      protocol    = "TCP"
    }

    type = "ClusterIP"
  }
}

# Create a Headless Service for StatefulSet
resource "kubernetes_service" "postgres_headless" {
  metadata {
    name      = "${var.cluster_name}-headless"
    namespace = var.namespace
    labels = merge(
      {
        app     = "postgres"
        cluster = var.cluster_name
      },
      var.labels
    )
  }

  spec {
    selector = {
      app     = "postgres"
      cluster = var.cluster_name
    }

    port {
      name        = "postgres"
      port        = 5432
      target_port = 5432
      protocol    = "TCP"
    }

    cluster_ip = "None"
  }
}
