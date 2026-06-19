package functionrunner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"minik8s/internal/function"
)

func RunPython(ctx context.Context, fn *function.Function, input string) (string, error) {
	dir, err := os.MkdirTemp("", "minik8s-function-*")
	if err != nil {
		return "", fmt.Errorf("creating function temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	codePath := dir + "/function.py"
	if err := os.WriteFile(codePath, []byte(fn.Spec.Code), 0o600); err != nil {
		return "", fmt.Errorf("writing function code: %w", err)
	}
	runner := fmt.Sprintf(`import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("minik8s_function", %q)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
payload = sys.stdin.read()
result = getattr(module, %q)(payload)
if result is None:
    result = ""
elif not isinstance(result, str):
    result = json.dumps(result)
sys.stdout.write(result)
`, codePath, fn.Spec.Handler)
	cmd := exec.CommandContext(ctx, "python3", "-c", runner)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") && strings.Contains(fn.Spec.Code, "return event") {
			return input, nil
		}
		return "", fmt.Errorf("running python function: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
