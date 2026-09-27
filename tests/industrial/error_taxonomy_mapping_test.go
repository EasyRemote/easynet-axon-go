// I-15 error_taxonomy_mapping
package industrial

import (
	"testing"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_error_taxonomy_mapping(t *testing.T) {
	if inv.ErrCancelled("x").Kind != inv.KindCancelled {
		t.Fatal("cancelled")
	}
	if inv.ErrDeadlineExceeded("x").Kind != inv.KindDeadlineExceeded {
		t.Fatal("deadline")
	}
	if inv.ErrUnavailable("x").Kind != inv.KindUnavailable {
		t.Fatal("unavailable")
	}
	if inv.ErrInvalidArgument("x").Kind != inv.KindInvalidArgument {
		t.Fatal("invalid")
	}
	if inv.ErrResourceExhausted("x").Kind != inv.KindResourceExhausted {
		t.Fatal("rex")
	}
	if inv.ErrPermissionDenied("x").Kind != inv.KindPermissionDenied {
		t.Fatal("perm")
	}
	if inv.ErrInternal("x").Kind != inv.KindInternal {
		t.Fatal("internal")
	}

	kind, reason := inv.MapProtoCode(1)
	if kind != inv.KindCancelled || reason != "" {
		t.Fatal("proto 1")
	}
	if k, _ := inv.MapProtoCode(3); k != inv.KindInvalidArgument {
		t.Fatal("3")
	}
	if k, _ := inv.MapProtoCode(4); k != inv.KindDeadlineExceeded {
		t.Fatal("4")
	}
	if k, _ := inv.MapProtoCode(7); k != inv.KindPermissionDenied {
		t.Fatal("7")
	}
	if k, _ := inv.MapProtoCode(8); k != inv.KindResourceExhausted {
		t.Fatal("8")
	}
	if k, _ := inv.MapProtoCode(13); k != inv.KindInternal {
		t.Fatal("13")
	}
	if k, _ := inv.MapProtoCode(14); k != inv.KindUnavailable {
		t.Fatal("14")
	}

	k, r := inv.MapProtoCode(99999)
	if k != inv.KindInternal || r != "unknown_error_code" {
		t.Fatalf("unknown: %v %q", k, r)
	}
}
