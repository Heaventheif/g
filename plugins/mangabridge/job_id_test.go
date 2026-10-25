package mangabridge

import "testing"

// ما يولّده الكود يجب أن يقبله الكود: معرّف createJob يطابق jobIDRe
// الذي يفرضه handleGetJobImage (كان 16 حرفاً مقابل {32} فيُرفض كل طلب صورة).
func TestCreateJobIDMatchesJobIDRe(t *testing.T) {
	s := New()
	for range 50 {
		j := s.createJob("one piece", "1")
		if !jobIDRe.MatchString(j.ID) {
			t.Fatalf("job ID %q لا يطابق jobIDRe", j.ID)
		}
	}
}

func TestRandomHexLength(t *testing.T) {
	for _, n := range []int{1, 15, 16, 32, 33} {
		if got := randomHex(n); len(got) != n {
			t.Errorf("randomHex(%d) طوله %d", n, len(got))
		}
	}
}
