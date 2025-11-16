output "postgres_namespace" {
  description = "The namespace where PostgreSQL clusters are deployed"
  value       = kubernetes_namespace.postgres.metadata[0].name
}

output "pgao_namespace" {
  description = "The namespace where PGAO is deployed"
  value       = kubernetes_namespace.pgao.metadata[0].name
}

output "prod_cluster_1_endpoint" {
  description = "Endpoint for prod-cluster-1"
  value       = "${module.postgres_cluster_prod_1.service_name}.${kubernetes_namespace.postgres.metadata[0].name}.svc.cluster.local:5432"
}

output "prod_cluster_2_endpoint" {
  description = "Endpoint for prod-cluster-2"
  value       = "${module.postgres_cluster_prod_2.service_name}.${kubernetes_namespace.postgres.metadata[0].name}.svc.cluster.local:5432"
}

output "dev_cluster_1_endpoint" {
  description = "Endpoint for dev-cluster-1"
  value       = "${module.postgres_cluster_dev_1.service_name}.${kubernetes_namespace.postgres.metadata[0].name}.svc.cluster.local:5432"
}

output "pgao_service_url" {
  description = "URL to access PGAO service"
  value       = "http://${kubernetes_service.pgao.metadata[0].name}.${kubernetes_namespace.pgao.metadata[0].name}.svc.cluster.local:8080"
}

output "pgao_nodeport" {
  description = "NodePort for accessing PGAO externally"
  value       = kubernetes_service.pgao.spec[0].port[0].node_port
}
