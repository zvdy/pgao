output "service_name" {
  description = "Name of the PostgreSQL service"
  value       = kubernetes_service.postgres.metadata[0].name
}

output "service_endpoint" {
  description = "Endpoint of the PostgreSQL service"
  value       = "${kubernetes_service.postgres.metadata[0].name}.${var.namespace}.svc.cluster.local"
}

output "headless_service_name" {
  description = "Name of the headless PostgreSQL service"
  value       = kubernetes_service.postgres_headless.metadata[0].name
}

output "cluster_name" {
  description = "Name of the PostgreSQL cluster"
  value       = var.cluster_name
}

output "namespace" {
  description = "Namespace of the PostgreSQL cluster"
  value       = var.namespace
}

output "replicas" {
  description = "Number of PostgreSQL replicas"
  value       = var.replicas
}
