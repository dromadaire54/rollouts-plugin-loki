package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/argoproj/argo-rollouts/utils/plugin/types"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/argoproj/argo-rollouts/metricproviders/plugin"
	"github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	"github.com/argoproj/argo-rollouts/utils/evaluate"
	metricutil "github.com/argoproj/argo-rollouts/utils/metric"
	timeutil "github.com/argoproj/argo-rollouts/utils/time"
	log "github.com/sirupsen/logrus"
)

type RpcPlugin struct {
	LogCtx log.Entry
}

type Config struct {
	// Address is the HTTP address and port of the loki server
	Address string `json:"address,omitempty" protobuf:"bytes,1,opt,name=address"`

	// Username for basic auth optional
	Username string `json:"username,omitempty" protobuf:"bytes,2,opt,name=username"`

	// Password for basic auth optional
	Password string `json:"password,omitempty" protobuf:"bytes,3,opt,name=password"`

	// Query is a raw loki query to perform
	Query string `json:"query,omitempty" protobuf:"bytes,2,opt,name=query"`
}

// Loki Query response
type QueryResponse struct {
	Status string `json:"status"`
	Data   Data   `json:"data"`
}

// Data response
type Data struct {
	ResultType string        `json:"resultType"`
	Result     []LokiResult  `json:"result"`
	Stats      []interface{} `json:"stats"`
}

// Loki Result Item
type LokiResult struct {
	Metric map[string]string `json:"metric,omitempty"`
	Value  []json.RawMessage `json:"value,omitempty"`
}

func (g *RpcPlugin) InitPlugin() types.RpcError {
	return types.RpcError{}
}

func (g *RpcPlugin) Run(analysisRun *v1alpha1.AnalysisRun, metric v1alpha1.Metric) v1alpha1.Measurement {
	startTime := timeutil.MetaNow()
	newMeasurement := v1alpha1.Measurement{
		StartedAt: &startTime,
	}
	client := http.Client{Timeout: time.Duration(10) * time.Second}
	config := Config{}
	response := QueryResponse{}

	err := json.Unmarshal(metric.Provider.Plugin["dromadaire54/rollouts-plugin-loki"], &config)
	if err != nil {
		return metricutil.MarkMeasurementError(newMeasurement, err)
	}

	reader := io.Reader(strings.NewReader(config.Query))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.Address, reader)
	if err != nil {
		return metricutil.MarkMeasurementError(newMeasurement, err)
	}
	if config.Username != "" && config.Password != "" {
		req.SetBasicAuth(config.Username, config.Password)
	}
	res, err := client.Do(req)

	if err != nil {
		return metricutil.MarkMeasurementError(newMeasurement, err)
	}
	body, err := io.ReadAll(res.Body)
	defer res.Body.Close()
	if res.StatusCode > 299 {
		return metricutil.MarkMeasurementError(newMeasurement, fmt.Errorf("error fetching metrics: %s", res.Status))
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return metricutil.MarkMeasurementError(newMeasurement, err)
	}

	newValue, newStatus, err := g.processResponse(metric, response)

	if err != nil {
		return metricutil.MarkMeasurementError(newMeasurement, err)
	}

	newMeasurement.Value = newValue
	newMeasurement.Phase = newStatus
	finishedTime := timeutil.MetaNow()
	newMeasurement.FinishedAt = &finishedTime
	return newMeasurement
}

func (g *RpcPlugin) Resume(analysisRun *v1alpha1.AnalysisRun, metric v1alpha1.Metric, measurement v1alpha1.Measurement) v1alpha1.Measurement {
	return measurement
}

func (g *RpcPlugin) Terminate(analysisRun *v1alpha1.AnalysisRun, metric v1alpha1.Metric, measurement v1alpha1.Measurement) v1alpha1.Measurement {
	return measurement
}

func (g *RpcPlugin) GarbageCollect(*v1alpha1.AnalysisRun, v1alpha1.Metric, int) types.RpcError {
	return types.RpcError{}
}

func (g *RpcPlugin) Type() string {
	return plugin.ProviderType
}

func (g *RpcPlugin) GetMetadata(metric v1alpha1.Metric) map[string]string {
	metricsMetadata := make(map[string]string)

	config := Config{}
	if err := json.Unmarshal(metric.Provider.Plugin["dromadaire54/rollouts-plugin-loki"], &config); err != nil {
		return nil
	}
	if config.Query != "" {
		metricsMetadata["ResolvedLokiQuery"] = config.Query
	}
	return metricsMetadata
}

// Process the loki response query
func (g *RpcPlugin) processResponse(metric v1alpha1.Metric, response QueryResponse) (string, v1alpha1.AnalysisPhase, error) {
	switch response.Data.ResultType {
	case "vector":
		results := make([]float64, 0, len(response.Data.Result))
		valueStr := "["
		for _, s := range response.Data.Result {
			if s.Value != nil {
				for _, v := range s.Value {
					itemValue := rawToString(v)
					valueFloat, err := strconv.ParseFloat(itemValue, 64)
					if err != nil {
						return "", v1alpha1.AnalysisPhaseError, err
					}
					valueStr = valueStr + itemValue + ","
					results = append(results, valueFloat)
				}
			}
		}
		// if we appended to the string, we should remove the last comma on the string
		if len(valueStr) > 1 {
			valueStr = valueStr[:len(valueStr)-1]
		}
		valueStr = valueStr + "]"
		newStatus, err := evaluate.EvaluateResult(results, metric, g.LogCtx)
		return valueStr, newStatus, err
	default:
		return "", v1alpha1.AnalysisPhaseError, fmt.Errorf("Loki log type not supported")
	}
}

// helper to decode RawMessage into string safely
func rawToString(r json.RawMessage) string {
	var s string
	if err := json.Unmarshal(r, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(r, &n); err == nil {
		return n.String()
	}
	return string(r)
}
