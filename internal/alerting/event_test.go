package alerting

import (
	"testing"
)

func TestAlertmanagerDecode(t *testing.T) {
	event, err := Alertmanager{}.Decode([]byte(`{"version":"4","status":"firing","groupKey":"g1","commonLabels":{"service":"api"},"alerts":[{"status":"firing","labels":{"alertname":"HighError","service":"api"},"annotations":{"summary":"错误率高"},"fingerprint":"fp"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	Normalize(event, "alertmanager")
	if event.Source != "alertmanager" || event.Status != "firing" || event.GroupKey != "g1" || event.Alerts[0].Labels["alertname"] != "HighError" {
		t.Fatalf("event = %+v", event)
	}
}

func TestGenericDecode(t *testing.T) {
	event, err := Generic{}.Decode([]byte(`{"id":"g-1","status":"firing","title":"登录故障","severity":"critical","service":"account","environment":"prod"}`))
	if err != nil {
		t.Fatal(err)
	}
	Normalize(event, "generic")
	if event.Source != "generic" || event.GroupKey != "g-1" || event.CommonLabels["severity"] != "critical" || event.Alerts[0].Labels["service"] != "account" {
		t.Fatalf("event = %+v", event)
	}
}

func TestPagerDutyDecode(t *testing.T) {
	event, err := PagerDuty{}.Decode([]byte(`{"event":{"event_type":"incident.triggered","data":{"id":"P1","title":"DB down","urgency":"high","service":{"summary":"db"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	Normalize(event, "pagerduty")
	if event.Status != "firing" || event.GroupKey != "P1" || event.CommonLabels["service"] != "db" {
		t.Fatalf("event = %+v", event)
	}
}

func TestRegistryRejectsUnknown(t *testing.T) {
	registry := NewRegistry(Alertmanager{})
	if _, err := registry.Decode("unknown", []byte(`{}`)); err == nil {
		t.Fatal("expected unknown source error")
	}
}
