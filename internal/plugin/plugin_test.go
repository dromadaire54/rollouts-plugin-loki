package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/argoproj/argo-rollouts/metricproviders/plugin/rpc"
	"k8s.io/utils/env"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	log "github.com/sirupsen/logrus"

	goPlugin "github.com/hashicorp/go-plugin"
)

const (
	BasicAuthCredentials = "myuser:mypassword"
)

var testHandshake = goPlugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "ARGO_ROLLOUTS_RPC_PLUGIN",
	MagicCookieValue: "metrics",
}

func pluginClient(t *testing.T) (rpc.MetricProviderPlugin, goPlugin.ClientProtocol, func(), chan struct{}) {
	logCtx := *log.WithFields(log.Fields{"plugin-test": "loki"})
	ctx, cancel := context.WithCancel(context.Background())

	rpcPluginImp := &RpcPlugin{
		LogCtx: logCtx,
	}

	// pluginMap is the map of plugins we can dispense.
	var pluginMap = map[string]goPlugin.Plugin{
		"RpcMetricProviderPlugin": &rpc.RpcMetricProviderPlugin{Impl: rpcPluginImp},
	}

	ch := make(chan *goPlugin.ReattachConfig, 1)
	closeCh := make(chan struct{})
	go goPlugin.Serve(&goPlugin.ServeConfig{
		HandshakeConfig: testHandshake,
		Plugins:         pluginMap,
		Test: &goPlugin.ServeTestConfig{
			Context:          ctx,
			ReattachConfigCh: ch,
			CloseCh:          closeCh,
		},
	})

	// We should get a config
	var config *goPlugin.ReattachConfig
	select {
	case config = <-ch:
	case <-time.After(2000 * time.Millisecond):
		t.Fatal("should've received reattach")
	}
	if config == nil {
		t.Fatal("config should not be nil")
	}

	// Connect!
	c := goPlugin.NewClient(&goPlugin.ClientConfig{
		Cmd:             nil,
		HandshakeConfig: testHandshake,
		Plugins:         pluginMap,
		Reattach:        config,
	})
	client, err := c.Client()
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Request the plugin
	raw, err := client.Dispense("RpcMetricProviderPlugin")
	if err != nil {
		t.Fail()
	}

	plugin, ok := raw.(rpc.MetricProviderPlugin)
	if !ok {
		t.Fail()
	}

	return plugin, client, cancel, closeCh
}

// This is just an example of how to test a plugin.
func TestRunSuccessfully(t *testing.T) {
	plugin, _, cancel, closeCh := pluginClient(t)
	defer cancel()
	lokiServer := mockLokiServer("")
	defer lokiServer.Close()

	err := plugin.InitPlugin()
	if err.Error() != "" {
		t.Fail()
	}

	msg := map[string]interface{}{
		"address":  env.GetString("LOKI_ADDRESS", lokiServer.URL),
		"username": env.GetString("LOKI_USERNAME", "myuser"),
		"password": env.GetString("LOKI_PASSWORD", "mypassword"),
		"query":    env.GetString("LOKI_QUERY", `sum(rate({cluster="tiime-preprod", namespace="chronos-development"} |= 'ERROR' [15m]))`),
	}

	jsonBytes, e := json.Marshal(msg)
	if e != nil {
		t.Fail()
	}

	jsonStr := string(jsonBytes)

	runMeasurement := plugin.Run(&v1alpha1.AnalysisRun{}, v1alpha1.Metric{
		Provider: v1alpha1.MetricProvider{
			Plugin: map[string]json.RawMessage{"dromadaire54/rollouts-plugin-loki": json.RawMessage(jsonStr)},
		},
		SuccessCondition: "result[len(result)-1] <= 1",
	})
	fmt.Println(runMeasurement)
	if string(runMeasurement.Phase) != "Successful" {
		t.Fail()
	}

	cancel()
	<-closeCh
}

func mockLokiServer(expectedAuthorizationHeader string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		log.StandardLogger().Infof("Received loki query")

		authorizationHeader := r.Header.Get("Authorization")
		// Reject call if we don't find the expected oauth token
		if (expectedAuthorizationHeader != "" && ("Bearer "+expectedAuthorizationHeader) != authorizationHeader) || (expectedAuthorizationHeader == "" && ("Basic "+base64.StdEncoding.EncodeToString([]byte(BasicAuthCredentials))) != authorizationHeader) {

			log.StandardLogger().Infof("Authorization header not as expected, rejecting")
			sc := http.StatusUnauthorized
			w.WriteHeader(sc)

		} else {
			log.StandardLogger().Infof("Authorization header as expected, continuing")
			lokiResponse := `{"status": "success", "data": {"resultType": "vector", "result": [{"metric": {}, "value": [1758802217.059, "0.07111111111111111"]}]}}`

			sc := http.StatusOK
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(sc)
			w.Write([]byte(lokiResponse))
		}
	}))
}
