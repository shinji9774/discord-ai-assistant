// Package ecsfake は ECS RunTask の HTTP fake を提供する。
package ecsfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

const (
	targetHeader           = "X-Amz-Target"
	runTaskTarget          = "AmazonEC2ContainerServiceV20141113.RunTask"
	contentType            = "application/x-amz-json-1.1"
	taskARNFormat          = "arn:aws:ecs:ap-northeast-1:000000000000:task/fake/%d"
	placementFailureReason = "RESOURCE:ENI"
	rejectedBody           = `{"__type":"ServerException","message":"rejected by fake"}`
)

// RunTaskRequest は fake が受けた RunTask 依頼。
type RunTaskRequest struct {
	Cluster        string
	TaskDefinition string
	LaunchType     string
	Count          int
	Subnets        []string
	SecurityGroups []string
	AssignPublicIP string
	Overrides      []ContainerOverride
}

// ContainerOverride は、コンテナごとの環境変数の上書き。
type ContainerOverride struct {
	Name        string
	Environment map[string]string
}

type keyValuePair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type containerOverrideInput struct {
	Name        string         `json:"name"`
	Environment []keyValuePair `json:"environment"`
}

type awsvpcConfiguration struct {
	Subnets        []string `json:"subnets"`
	SecurityGroups []string `json:"securityGroups"`
	AssignPublicIP string   `json:"assignPublicIp"`
}

type runTaskInput struct {
	Cluster              string `json:"cluster"`
	TaskDefinition       string `json:"taskDefinition"`
	LaunchType           string `json:"launchType"`
	Count                int    `json:"count"`
	NetworkConfiguration struct {
		AwsvpcConfiguration awsvpcConfiguration `json:"awsvpcConfiguration"`
	} `json:"networkConfiguration"`
	Overrides struct {
		ContainerOverrides []containerOverrideInput `json:"containerOverrides"`
	} `json:"overrides"`
}

type task struct {
	TaskARN string `json:"taskArn"`
}

type failure struct {
	Reason string `json:"reason"`
}

type runTaskOutput struct {
	Tasks    []task    `json:"tasks"`
	Failures []failure `json:"failures"`
}

// Server は POST / （X-Amz-Target: AmazonEC2ContainerServiceV20141113.RunTask）に応える http.Handler。
type Server struct {
	mu              sync.Mutex
	failStatus      int
	rejectPlacement bool
	requests        []RunTaskRequest
}

func New() *Server {
	return &Server{}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get(targetHeader) != runTaskTarget {
		http.NotFound(w, r)
		return
	}
	var in runTaskInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, in.toRequest())
	w.Header().Set("Content-Type", contentType)
	if s.failStatus != 0 {
		w.WriteHeader(s.failStatus)
		_, _ = w.Write([]byte(rejectedBody))
		return
	}
	_ = json.NewEncoder(w).Encode(s.output())
}

// output は、設定に応じた RunTask の応答を作る。s.mu を持った状態で呼ぶ。
func (s *Server) output() runTaskOutput {
	if s.rejectPlacement {
		return runTaskOutput{Tasks: []task{}, Failures: []failure{{Reason: placementFailureReason}}}
	}
	return runTaskOutput{Tasks: []task{{TaskARN: fmt.Sprintf(taskARNFormat, len(s.requests))}}, Failures: []failure{}}
}

func (in runTaskInput) toRequest() RunTaskRequest {
	vpc := in.NetworkConfiguration.AwsvpcConfiguration
	overrides := make([]ContainerOverride, 0, len(in.Overrides.ContainerOverrides))
	for _, o := range in.Overrides.ContainerOverrides {
		env := make(map[string]string, len(o.Environment))
		for _, kv := range o.Environment {
			env[kv.Name] = kv.Value
		}
		overrides = append(overrides, ContainerOverride{Name: o.Name, Environment: env})
	}
	return RunTaskRequest{
		Cluster: in.Cluster, TaskDefinition: in.TaskDefinition, LaunchType: in.LaunchType, Count: in.Count,
		Subnets: vpc.Subnets, SecurityGroups: vpc.SecurityGroups, AssignPublicIP: vpc.AssignPublicIP,
		Overrides: overrides,
	}
}

// FailWith は以後の応答を指定の status の失敗にする。
func (s *Server) FailWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = status
}

// RejectPlacement は以後の応答を、タスクを起動せず配置の失敗を返すものにする（HTTP は 200）。
func (s *Server) RejectPlacement() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectPlacement = true
}

func (s *Server) Requests() []RunTaskRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RunTaskRequest(nil), s.requests...)
}

// Reset は、失敗の設定と依頼の記録を消す。
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = 0
	s.rejectPlacement = false
	s.requests = nil
}
