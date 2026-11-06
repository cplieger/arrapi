package radarr_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/radarr"
)

func ExampleCheck() {
	info, err := radarr.Check(context.Background(), "http://radarr:7878", "your-api-key")
	if ce, ok := errors.AsType[*arrapi.CheckError](err); ok {
		switch ce.Reason {
		case arrapi.CheckKey:
			fmt.Println("api_key:", ce)
		case arrapi.CheckURL, arrapi.CheckBasePath, arrapi.CheckWrongApp, arrapi.CheckNotArrAPI:
			fmt.Println("url:", ce)
		default:
			fmt.Println(ce)
		}
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Connected: Radarr %s over API %s\n", info.Version, info.API)
}
