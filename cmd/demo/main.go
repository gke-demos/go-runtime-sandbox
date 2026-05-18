/*
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// cmd/demo is a thin CLI wrapper around pkg/goruntime. It demonstrates
// shipping Go source into a Kubernetes Agent Sandbox and executing it,
// across two flows: a single-file smoke run and a multi-file module.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	samples "github.com/gke-demos/go-runtime-sandbox"
	"github.com/gke-demos/go-runtime-sandbox/pkg/goruntime"
)

func main() {
	ns := flag.String("namespace", "default", "Kubernetes namespace")
	tpl := flag.String("template", "go-runtime-template", "SandboxTemplate name")
	flow := flag.String("flow", "all", "all | smoke | multi")
	claim := flag.String("claim", "", "reattach to an existing sandbox by claim name")
	keep := flag.Bool("keep", false, "Disconnect (preserve sandbox) on exit instead of Close")
	openTimeout := flag.Duration("open-timeout", 5*time.Minute, "max time to wait for Open")
	flag.Parse()

	if err := run(*ns, *tpl, *flow, *claim, *keep, *openTimeout); err != nil {
		log.Fatalf("demo: %v", err)
	}
}

func run(namespace, template, flow, claim string, keep bool, openTimeout time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()

	fmt.Printf("==> opening sandbox (namespace=%s template=%s claim=%q)\n", namespace, template, claim)
	rt, err := goruntime.Open(openCtx, goruntime.Options{
		Namespace: namespace,
		Template:  template,
		ClaimName: claim,
	})
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	fmt.Printf("    claim=%s\n", rt.ClaimName())

	defer func() {
		// Use a fresh context so teardown still runs if ctx is canceled.
		shutdownCtx, c := context.WithTimeout(context.Background(), 60*time.Second)
		defer c()
		if keep {
			fmt.Printf("==> disconnecting (sandbox %s preserved)\n", rt.ClaimName())
			if err := rt.Disconnect(shutdownCtx); err != nil {
				log.Printf("disconnect: %v", err)
			}
			return
		}
		fmt.Printf("==> closing (deleting sandbox %s)\n", rt.ClaimName())
		if err := rt.Close(shutdownCtx); err != nil {
			log.Printf("close: %v", err)
		}
	}()

	switch flow {
	case "all":
		if err := smokeFlow(ctx, rt); err != nil {
			return err
		}
		if err := multiFlow(ctx, rt); err != nil {
			return err
		}
	case "smoke":
		if err := smokeFlow(ctx, rt); err != nil {
			return err
		}
	case "multi":
		if err := multiFlow(ctx, rt); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown --flow=%q (want all|smoke|multi)", flow)
	}
	fmt.Println("==> PoC complete")
	return nil
}

func smokeFlow(ctx context.Context, rt *goruntime.Session) error {
	fmt.Println("\n=== single-file smoke flow ===")
	files, err := samples.LoadModule("smoke")
	if err != nil {
		return err
	}

	res, err := rt.Execute(ctx, goruntime.Request{
		Files:   files,
		Command: "go run main.go",
	})
	if err != nil {
		return fmt.Errorf("smoke go run: %w", err)
	}
	printResult("go run main.go", res)
	if res.ExitCode != 0 {
		return fmt.Errorf("smoke go run exited %d", res.ExitCode)
	}

	res, err = rt.Execute(ctx, goruntime.Request{Command: "go build -o app main.go"})
	if err != nil {
		return fmt.Errorf("smoke go build: %w", err)
	}
	printResult("go build -o app main.go", res)
	if res.ExitCode != 0 {
		return fmt.Errorf("smoke go build exited %d", res.ExitCode)
	}

	res, err = rt.Execute(ctx, goruntime.Request{Command: "./app"})
	if err != nil {
		return fmt.Errorf("smoke ./app: %w", err)
	}
	printResult("./app  (re-runs the binary built in the previous call)", res)
	if res.ExitCode != 0 {
		return fmt.Errorf("smoke ./app exited %d", res.ExitCode)
	}
	return nil
}

func multiFlow(ctx context.Context, rt *goruntime.Session) error {
	fmt.Println("\n=== multi-file module flow ===")
	files, err := samples.LoadModule("multifile")
	if err != nil {
		return err
	}

	if _, err := rt.Execute(ctx, goruntime.Request{Command: "rm -rf -- * .[!.]* 2>/dev/null; true"}); err != nil {
		return fmt.Errorf("clean slate: %w", err)
	}

	res, err := rt.Execute(ctx, goruntime.Request{
		Files:   files,
		Command: "go build -o app .",
	})
	if err != nil {
		return fmt.Errorf("multi build: %w", err)
	}
	printResult("go build -o app .", res)
	if res.ExitCode != 0 {
		return fmt.Errorf("multi build exited %d", res.ExitCode)
	}

	res, err = rt.Execute(ctx, goruntime.Request{Command: "./app"})
	if err != nil {
		return fmt.Errorf("multi run: %w", err)
	}
	printResult("./app", res)
	if res.ExitCode != 0 {
		return fmt.Errorf("multi run exited %d", res.ExitCode)
	}

	res, err = rt.Execute(ctx, goruntime.Request{Command: "go test ./..."})
	if err != nil {
		return fmt.Errorf("multi test: %w", err)
	}
	printResult("go test ./...", res)
	if res.ExitCode != 0 {
		return fmt.Errorf("multi test exited %d", res.ExitCode)
	}
	return nil
}

func printResult(label string, res *goruntime.Result) {
	fmt.Printf("\n-- %s [exit=%d, %s] --\n", label, res.ExitCode, res.Duration.Round(time.Millisecond))
	if res.Stdout != "" {
		fmt.Printf("stdout:\n%s", ensureNL(res.Stdout))
		if res.StdoutTruncated {
			fmt.Println("[stdout truncated]")
		}
	}
	if res.Stderr != "" {
		fmt.Printf("stderr:\n%s", ensureNL(res.Stderr))
		if res.StderrTruncated {
			fmt.Println("[stderr truncated]")
		}
	}
}

func ensureNL(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
