server:
  host: "0.0.0.0"
  port: 8080
  read_timeout: 15s
  write_timeout: 15s
  idle_timeout: 60s

clusters:
%{ for cluster in clusters ~}
  - id: "${cluster.id}"
    name: "${cluster.name}"
    host: "${cluster.host}"
    port: ${cluster.port}
    user: "postgres"
    password: "$${DATABASE_PASSWORD}"
    database: "${cluster.database}"
    ssl_mode: "disable"
    max_connections: 25
    min_connections: 5
    conn_max_lifetime: 1h
    conn_max_idle_time: 30m
    region: "local"
    environment: "kubernetes"
%{ endfor ~}

logging:
  level: "info"
  format: "json"
  output: "stdout"

metrics:
  collection_interval: 60s
  retention_days: 30
  enable_prometheus: true
  prometheus_port: 9090

aws:
  region: "local"
