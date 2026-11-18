package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

// approvalAgent 模拟 pi 在运行中申请受控动作。
type approvalAgent struct {
	approvals []json.RawMessage
	err       error
}

func (a approvalAgent) Run(_ context.Context, _ agent.Input) (agent.Output, error) {
	if a.err != nil {
		return agent.Output{}, a.err
	}
	return agent.Output{Text: "已申请", Approvals: a.approvals}, nil
}

func caseInbound(id, text, caseID string) channel.Inbound {
	in := inbound(id, text)
	in.CaseID = caseID
	return in
}

func TestApprovalSinkReceivesRequests(t *testing.T) {
	raw := json.RawMessage(`{"action":"k8s_scale","namespace":"prod","deployment":"checkout","replicas":3}`)
	eng := newEngine(t, &captureChannel{}, approvalAgent{approvals: []json.RawMessage{raw}})

	var gotCase string
	var got []json.RawMessage
	eng.SetApprovalSink(func(_ context.Context, caseID string, item json.RawMessage) error {
		gotCase = caseID
		got = append(got, item)
		return nil
	})

	if _, err := eng.Submit(context.Background(), caseInbound("om_ap1", "扩容", "case_1")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotCase != "case_1" {
		t.Fatalf("case = %q", gotCase)
	}
	if len(got) != 1 || string(got[0]) != string(raw) {
		t.Fatalf("approvals = %s", got)
	}
}

// 没有关联案件时不能落审批：审批必须挂在案件上才有人能处理。
func TestApprovalSinkSkippedWithoutCase(t *testing.T) {
	raw := json.RawMessage(`{"action":"k8s_restart","namespace":"prod","deployment":"checkout"}`)
	eng := newEngine(t, &captureChannel{}, approvalAgent{approvals: []json.RawMessage{raw}})

	calls := 0
	eng.SetApprovalSink(func(context.Context, string, json.RawMessage) error {
		calls++
		return nil
	})
	if _, err := eng.Submit(context.Background(), inbound("om_ap2", "重启")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("无案件时不应回写审批，调用 %d 次", calls)
	}
}

// 运行失败时申请一并丢弃，避免「没诊断完就申请动作」。
func TestApprovalSinkSkippedOnFailure(t *testing.T) {
	raw := json.RawMessage(`{"action":"k8s_restart","namespace":"prod","deployment":"checkout"}`)
	eng := newEngine(t, &captureChannel{}, approvalAgent{approvals: []json.RawMessage{raw}, err: context.DeadlineExceeded})

	calls := 0
	eng.SetApprovalSink(func(context.Context, string, json.RawMessage) error {
		calls++
		return nil
	})
	if _, err := eng.Submit(context.Background(), caseInbound("om_ap3", "重启", "case_2")); err != nil {
		t.Fatal(err)
	}
	_ = eng.Flush(context.Background())
	if calls != 0 {
		t.Fatalf("失败运行不应回写审批，调用 %d 次", calls)
	}
}
