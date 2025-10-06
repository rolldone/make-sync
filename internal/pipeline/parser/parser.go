package parser

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"make-sync/internal/pipeline/types"
)

// ParsePipeline parses a pipeline YAML file
func ParsePipeline(filePath string) (*types.Pipeline, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read pipeline file: %v", err)
	}

	var pipeline types.Pipeline
	if err := yaml.Unmarshal(data, &pipeline); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline YAML: %v", err)
	}

	// Initialize context variables map
	pipeline.ContextVariables = make(map[string]string)

	return &pipeline, nil
}

// ParseVars parses a vars YAML file and returns vars for a specific key
func ParseVars(filePath, key string) (types.Vars, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read vars file: %v", err)
	}

	var allVars map[string]types.Vars
	if err := yaml.Unmarshal(data, &allVars); err != nil {
		return nil, fmt.Errorf("failed to parse vars YAML: %v", err)
	}

	vars, exists := allVars[key]
	if !exists {
		return nil, fmt.Errorf("vars key '%s' not found", key)
	}

	return vars, nil
}

// LoadExecutions loads executions from config (placeholder for now)
func LoadExecutions(configPath string) ([]types.Execution, error) {
	// TODO: Load from make-sync.yaml direct_access.executions
	return []types.Execution{}, nil
}

// ResolvePipelinePath resolves pipeline file path relative to pipeline_dir
func ResolvePipelinePath(pipelineDir, pipelineFile string) string {
	return filepath.Join(pipelineDir, pipelineFile)
}

// ResolveVarsPath resolves vars file path
func ResolveVarsPath(pipelineDir string) string {
	return filepath.Join(pipelineDir, "vars.yaml")
}
