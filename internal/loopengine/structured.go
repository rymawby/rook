package loopengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/rymawby/rook/internal/backend"
)

// runStructured implements the file-based structured-output contract
// (§6.1): the prompt tells the backend to write its answer as JSON to
// outPath; Rook then reads and validates it. One corrective retry is
// attempted (same context plus the validation error) if the file is
// missing or invalid; a second failure fails the step.
func runStructured[T any](ctx context.Context, be backend.Backend, req backend.TaskRequest, outPath, schemaHint string) (T, backend.TaskResult, error) {
	var zero T

	req.Prompt = withStructuredInstructions(req.Prompt, outPath, schemaHint)
	req.Attempt = 1
	res, err := be.RunTask(ctx, req)
	if err != nil {
		return zero, res, fmt.Errorf("structured call: %w", err)
	}

	v, parseErr := readStructured[T](outPath)
	if parseErr == nil {
		return v, res, nil
	}

	// One corrective retry, per §6.1 step 3.
	req.Attempt = 2
	req.Prompt = req.Prompt + fmt.Sprintf(
		"\n\n---\nYour previous attempt's output at %s was missing or invalid: %v\nWrite ONLY valid JSON matching the schema to that exact path now.\n",
		outPath, parseErr,
	)
	res2, err := be.RunTask(ctx, req)
	if err != nil {
		return zero, res2, fmt.Errorf("structured call (retry): %w", err)
	}
	v, parseErr = readStructured[T](outPath)
	if parseErr != nil {
		return zero, res2, fmt.Errorf("structured call: invalid output after corrective retry: %w", parseErr)
	}
	return v, res2, nil
}

func readStructured[T any](path string) (T, error) {
	var v T
	data, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}

func withStructuredInstructions(prompt, outPath, schemaHint string) string {
	return fmt.Sprintf(
		"%s\n\n---\nWrite your answer as a single JSON document to the file %q (create parent directories if needed, overwrite if it already exists). Do not print the JSON to stdout — write it to that file. The JSON must match this shape:\n%s\n",
		prompt, outPath, schemaHint,
	)
}
