package app

import "testing"

func TestValidateProjectSlug(t *testing.T) {
	valid := []Project{
		"default",
		"ref",
		"christian",
		"christian-after",
		"a",
		"a1",
		"2026-08-16",
	}
	for _, slug := range valid {
		err := ValidateProjectSlug(slug)
		if err != nil {
			t.Errorf("ValidateProjectSlug(%q) = %v, want nil", slug, err)
		}
	}

	invalidSlugs := []Project{
		"",          // empty
		".",         // traversal
		"..",        // traversal
		"a/b",       // separator
		"/abs",      // absolute
		`a\b`,       // windows separator
		"Christian", // uppercase
		"has space", // space
		"has.dot",   // dot
		"has_score", // underscore
		"-lead",     // leading dash
		"trail-",    // trailing dash
		"all",       // reserved: "no filter" sentinel
		"jobs",      // reserved
		"projects",  // reserved
		"saved",     // reserved
		"a\x00b",    // NUL
		"café",      // non-ASCII
	}
	for _, slug := range invalidSlugs {
		err := ValidateProjectSlug(slug)
		if err == nil {
			t.Errorf("ValidateProjectSlug(%q) = nil, want error", slug)
		}
	}

	// The length limit is inclusive, so both sides of the boundary are checked.
	atLimit := Project("")
	for range MaxProjectSlugLen {
		atLimit += "a"
	}

	err := ValidateProjectSlug(atLimit)
	if err != nil {
		t.Errorf("ValidateProjectSlug(%d chars) = %v, want nil", len(atLimit), err)
	}

	long := atLimit + "a"

	err = ValidateProjectSlug(long)
	if err == nil {
		t.Errorf("ValidateProjectSlug(%d chars) = nil, want error", len(long))
	}
}
