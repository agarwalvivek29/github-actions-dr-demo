package main

// Minimal in-cluster Kubernetes client: the controller only needs to read a
// ConfigMap, create Secrets and PipelineRuns, and read PipelineRun status.

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

type Kube struct {
	host string
	http *http.Client
}

func NewKube() (*Kube, error) {
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("not running in a cluster? %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	return &Kube{
		host: "https://" + os.Getenv("KUBERNETES_SERVICE_HOST") + ":" + os.Getenv("KUBERNETES_SERVICE_PORT"),
		http: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}},
	}, nil
}

type notFound struct{ error }

func (k *Kube) do(method, path, contentType string, body, out any) error {
	tok, err := os.ReadFile(saDir + "/token") // re-read: projected tokens rotate
	if err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, k.host+path, rdr)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := k.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return notFound{fmt.Errorf("%s %s: not found", method, path)}
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (k *Kube) ConfigMap(ns, name string) (map[string]string, error) {
	var cm struct {
		Data map[string]string `json:"data"`
	}
	err := k.do("GET", fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", ns, name), "", nil, &cm)
	return cm.Data, err
}

type obj = map[string]any

type PipelineRun struct {
	Metadata struct {
		Name   string            `json:"name"`
		UID    string            `json:"uid"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Conditions []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
		StartTime      *time.Time `json:"startTime"`
		CompletionTime *time.Time `json:"completionTime"`
	} `json:"status"`
}

// Done reports (finished, succeeded, reason).
func (p PipelineRun) Done() (bool, bool, string) {
	for _, c := range p.Status.Conditions {
		if c.Type == "Succeeded" {
			return c.Status != "Unknown", c.Status == "True", c.Reason
		}
	}
	return false, false, "Pending"
}

func (k *Kube) prPath(ns string) string {
	return fmt.Sprintf("/apis/tekton.dev/v1/namespaces/%s/pipelineruns", ns)
}

func (k *Kube) ListPipelineRuns(ns, selector string) ([]PipelineRun, error) {
	var list struct {
		Items []PipelineRun `json:"items"`
	}
	err := k.do("GET", k.prPath(ns)+"?labelSelector="+selector, "", nil, &list)
	return list.Items, err
}

func (k *Kube) GetPipelineRun(ns, name string) (PipelineRun, error) {
	var pr PipelineRun
	err := k.do("GET", k.prPath(ns)+"/"+name, "", nil, &pr)
	return pr, err
}

func (k *Kube) CreatePipelineRun(ns string, pr obj) (PipelineRun, error) {
	var out PipelineRun
	err := k.do("POST", k.prPath(ns), "", pr, &out)
	return out, err
}

func (k *Kube) CreateSecret(ns string, s obj) error {
	return k.do("POST", fmt.Sprintf("/api/v1/namespaces/%s/secrets", ns), "", s, nil)
}

func (k *Kube) PatchSecret(ns, name string, patch obj) error {
	return k.do("PATCH", fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", ns, name), "application/merge-patch+json", patch, nil)
}
