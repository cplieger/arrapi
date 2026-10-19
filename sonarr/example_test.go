package sonarr_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/sonarr"
)

func ExampleNew() {
	c, err := sonarr.New("http://sonarr:8989", "your-api-key", sonarr.WithMaxAttempts(5))
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Sonarr %s over API %s\n", info.Version, info.API)
	series, err := c.Series(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range series {
		if !s.Order.Official {
			fmt.Printf("%s is in %q: its seasons are not TheTVDB's aired order\n", s.Title, s.Order.Name)
		}
	}
}

func ExampleClient_Info() {
	c, err := sonarr.New("http://sonarr:8989", "your-api-key")
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	info, err := c.Info(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Sonarr %s over API %s (deprecated: %t)\n", info.Version, info.API, info.Deprecated)
	if info.Features.SeriesOrders {
		fmt.Println("Series.Order can name an order other than TheTVDB's aired one")
	}
}

func ExampleClient_Pin() {
	c, err := sonarr.New("http://sonarr:8989", "your-api-key")
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	// Every read through p comes from one server version, so a series and its
	// episodes are numbered in the same order even across an upgrade.
	p, err := c.Pin(ctx)
	if err != nil {
		log.Fatal(err)
	}
	series, err := p.Series(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range series {
		episodes, err := p.Episodes(ctx, s.ID)
		if _, changed := errors.AsType[*arrapi.ServerChangedError](err); changed {
			log.Fatalf("Sonarr changed version during the pass; read it again: %v", err)
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s.Title, len(episodes), "episodes")
	}
}

func ExampleClient_HistorySince() {
	c, err := sonarr.New("http://sonarr:8989", "your-api-key")
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	recs, err := c.HistorySince(context.Background(), time.Now().Add(-24*time.Hour),
		sonarr.EventDownloadImported, sonarr.EventFolderImported)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(recs), "imports in the last day")
}
