package models

type Cluster struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Status      string `json:"status"`
    Configuration map[string]interface{} `json:"configuration"`
    Metrics     map[string]float64 `json:"metrics"`
}

// NewCluster creates a new Cluster instance
func NewCluster(id, name, status string, configuration map[string]interface{}) *Cluster {
    return &Cluster{
        ID:          id,
        Name:        name,
        Status:      status,
        Configuration: configuration,
        Metrics:     make(map[string]float64),
    }
}

// UpdateStatus updates the status of the cluster
func (c *Cluster) UpdateStatus(status string) {
    c.Status = status
}

// AddMetric adds a performance metric to the cluster
func (c *Cluster) AddMetric(key string, value float64) {
    c.Metrics[key] = value
}