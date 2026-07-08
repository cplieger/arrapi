package arrapi_test

import (
	"context"
	"fmt"

	"github.com/cplieger/arrapi"
)

// ExampleTagIDs shows resolving tag labels to IDs and using the result to
// filter items by tag.
func ExampleTagIDs() {
	tags := []arrapi.Tag{
		{ID: 1, Label: "anime"},
		{ID: 2, Label: "4k"},
		{ID: 3, Label: "kids"},
	}
	want := arrapi.TagIDs(tags, "anime", "kids")

	fmt.Println(arrapi.HasAnyTag([]int{3, 9}, want)) // carries "kids"
	fmt.Println(arrapi.HasAnyTag([]int{2}, want))    // only "4k"
	// Output:
	// true
	// false
}

// ExampleNewSonarr shows the construct → ping → fetch flow. It is compiled as
// documentation but not run (no Output comment), so it needs no live instance.
func ExampleNewSonarr() {
	ctx := context.Background()

	sonarr, err := arrapi.NewSonarr("http://sonarr:8989", "your-api-key")
	if err != nil {
		return
	}
	defer sonarr.Close()

	if err := sonarr.Ping(ctx); err != nil {
		return
	}
	series, err := sonarr.GetSeries(ctx)
	if err != nil {
		return
	}
	fmt.Printf("%d series\n", len(series))
}
