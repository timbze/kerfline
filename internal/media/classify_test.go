package media

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want Class
	}{
		{"save this in the medical records", ClassSave},
		{"what does this x-ray show?", ClassLook},
		{"Save it and tell me what it says", ClassMixed},
		{"tooth x-ray 2026-09-01", ClassSave},
		{"what does this medical x-ray show?", ClassLook},
		{"save this?", ClassSave},
		{"file this under scans", ClassSave},
		{"look at this then file it", ClassMixed},
		{"transcribe this", ClassLook},
		{"keep this", ClassSave},
		{"profile of the tooth", ClassSave},
		{"can you see a cavity?", ClassLook},
		{"put this in records", ClassSave},
		{"describe then save", ClassMixed},
		{"what's in this", ClassLook},
		{"archive this pdf", ClassSave},
		{"extract the date from this", ClassLook},
		{"this is an x-ray of a tooth for this person and I want it saved in their medical records", ClassSave},
		{"/ask", ClassSave},
		{"what folder should I put this in?", ClassSave},
		{"what's the right path for this?", ClassSave},
		{"I already read the discharge papers; file this", ClassSave},
		{"save this, what filename?", ClassSave},
		{"what's this", ClassLook},
		{"what's that", ClassLook},
		{"looking at this x-ray", ClassLook},
		{"whats this", ClassLook},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := Classify(tc.in)
			if got != tc.want {
				t.Fatalf("Classify(%q)=%s want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestAttachVision(t *testing.T) {
	cases := []struct {
		class    Class
		mime     string
		vision   string
		attach   bool
		skipLook bool
	}{
		{ClassSave, "image/jpeg", VisionAuto, false, false},
		{ClassLook, "image/jpeg", VisionAuto, true, false},
		{ClassMixed, "image/jpeg", VisionAuto, true, false},
		{ClassLook, "application/pdf", VisionAuto, false, false},
		{ClassLook, "image/jpeg", VisionNever, false, true},
		{ClassMixed, "image/jpeg", VisionNever, false, false},
		{ClassSave, "image/jpeg", VisionAlways, true, false},
		{ClassLook, "image/png", VisionAlways, true, false},
		{ClassSave, "application/pdf", VisionAlways, false, false},
	}
	for _, tc := range cases {
		attach, skip := AttachVision(tc.class, tc.mime, tc.vision)
		if attach != tc.attach || skip != tc.skipLook {
			t.Fatalf("%s %s %s: attach=%v skip=%v want %v %v",
				tc.class, tc.mime, tc.vision, attach, skip, tc.attach, tc.skipLook)
		}
	}
}

func TestVisionMIME(t *testing.T) {
	if !VisionMIME("image/jpeg") || !VisionMIME("image/png") {
		t.Fatal("jpeg/png should be vision")
	}
	for _, m := range []string{"application/pdf", "image/webp", "image/heic", "", "application/octet-stream"} {
		if VisionMIME(m) {
			t.Fatalf("%q should not be vision in v1", m)
		}
	}
}
