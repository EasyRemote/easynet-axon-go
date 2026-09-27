// I-10 message_inbox_fifo
package industrial

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_message_inbox_fifo(t *testing.T) {
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "echo", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		observed := make([]int64, 0, 100)
		for i := 0; i < 100; i++ {
			msg := ac.RecvMessage(ctx, 5*time.Second)
			if msg == nil {
				return nil, inv.ErrInternal("timeout")
			}
			var body map[string]int64
			_ = json.Unmarshal(msg.Payload, &body)
			observed = append(observed, body["seq"])
		}
		b, _ := json.Marshal(observed)
		return b, nil
	})
	h, _ := invokeIndustrial(t, context.Background(), rt, key, "echo", nil, "", nil)
	for i := 0; i < 100; i++ {
		payload, _ := json.Marshal(map[string]int{"seq": i})
		if _, e := rt.SendMessage(context.Background(), h.InvocationID(), payload, ""); e != nil {
			t.Fatal(e)
		}
	}
	h.Wait(context.Background())
	events := rt.CoreOf(h.InvocationID()).SnapshotEvents()
	var completed *inv.InvocationEvent
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType == "completed" {
			completed = events[i]
			break
		}
	}
	if completed == nil {
		t.Fatal("no completed event")
	}
	var got []int64
	_ = json.Unmarshal(completed.Payload, &got)
	for i, v := range got {
		if v != int64(i) {
			t.Fatalf("FIFO breach at %d: got %d", i, v)
		}
	}
}
