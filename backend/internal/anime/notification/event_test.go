package notification

import "testing"

func TestFailureLabelsDescribeConfirmedState(t *testing.T) {
	for _, tc := range []struct {
		code string
		want string
	}{
		{"TASK_READ_FAILED", "持续异常"},
		{"TASK_RATE_LIMITED", "持续异常"},
		{"ORGANIZE_READ_FAILED", "持续异常"},
		{"SUBMIT_UNCERTAIN", "需要复核"},
		{"DIRECTORY_CREATE_UNCERTAIN", "需要复核"},
		{"ORGANIZE_UNCERTAIN", "需要复核"},
		{"TASK_TARGET_CONFLICT", "任务异常"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			event := Sample("failed")
			event.BlockCode = tc.code
			if values := Values(event); values["event"] != tc.want || values["reason"] == "" {
				t.Fatal(values)
			}
		})
	}
}
