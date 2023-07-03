package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type Resource struct {
	Name       string `json:"name"`
	ShortNames string `json:"shortNames"`
	APIVersion string `json:"apiVersion"`
	Namespaced bool   `json:"namespaced"`
	Kind       string `json:"kind"`
}

type CommandOutput struct {
	ResourceName string
	CommandLog   string
	Stdout       string
	Stderr       string
}

func main() {
	var context string

	outputDir := flag.String("output", "resources", "Output directory for logs")
	maxChannels := flag.Int("max-channels", 2, "Maximum number of concurrent goroutines")
	flag.StringVar(&context, "context", "", "Use kubectl context")
	flag.Parse()
	command := []string{"kubectl", "api-resources"}

	if context == "" {
		fmt.Println("Using default context")
	} else {
		command = append(command, "--context", context)
	}

	cmd := exec.Command(command[0], command[1:]...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Printf("Error creating stdout pipe: %v", err)
		return
	}

	err = cmd.Start()
	if err != nil {
		fmt.Printf("Error starting command: %v", err)
		return
	}

	resourceList := make([]Resource, 0)
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) == 4 {
			resource := Resource{
				Name:       fields[0],
				APIVersion: fields[1],
				Namespaced: fields[2] == "true",
				Kind:       fields[3],
			}
			resourceList = append(resourceList, resource)
		}
		if len(fields) >= 5 {
			resource := Resource{
				Name:       fields[0],
				ShortNames: fields[1],
				APIVersion: fields[2],
				Namespaced: fields[3] == "true",
				Kind:       fields[4],
			}
			resourceList = append(resourceList, resource)
		}
	}

	err = cmd.Wait()
	if err != nil {
		fmt.Printf("Error waiting for command: %v", err)
		return
	}

	err = exploreProcessResources(resourceList, *outputDir, *maxChannels, context)
	if err != nil {
		fmt.Printf("Error processing resources: %v\n", err)
		return
	}

	err = concatenateLogs(*outputDir)
	if err != nil {
		fmt.Printf("Error concatenating logs: %v\n", err)
		return
	}
}

func exploreProcessResources(resources []Resource, outputDir string, maxChannels int, context string) error {
	fmt.Printf("Parsed resources:\n")

	// Create a channel to control the number of concurrent goroutines
	concurrency := make(chan struct{}, maxChannels)

	// Create a channel to collect errors from goroutines
	errCh := make(chan error)

	// Create a wait group to wait for all goroutines to finish
	var wg sync.WaitGroup

	for _, resource := range resources {
		wg.Add(1)

		// Launch a goroutine to process each resource concurrently
		go func(resource Resource) {
			defer wg.Done()

			concurrency <- struct{}{} // Acquire a slot in the concurrency channel
			defer func() {
				<-concurrency // Release the slot in the concurrency channel
			}()

			output := processResource(resource, outputDir, context)
			if output.Stderr != "" {
				errCh <- fmt.Errorf("error processing resource %s: %v", resource.Name, output.Stderr)
			}
		}(resource)
	}

	// Start a goroutine to wait for all goroutines to finish and close the error channel
	go func() {
		wg.Wait()
		close(errCh)
	}()

	// Collect errors from the error channel
	for err := range errCh {
		fmt.Println(err)
	}

	return nil
}

func processResource(resource Resource, outputDir string, context string) CommandOutput {
	command := "kubectl"
	cmdArgs := []string{"get", "--all-namespaces", resource.Name}
	if context != "" {
		cmdArgs = append(cmdArgs, "--context", context)
	}
	joined := strings.Join(cmdArgs, " ")
	commandLog := fmt.Sprintf("Running command: %s %s\n", command, joined)

	output := CommandOutput{
		ResourceName: resource.Name,
		CommandLog:   commandLog,
	}

	err := os.MkdirAll(outputDir, os.ModePerm)
	if err != nil {
		output.Stderr = fmt.Sprintf("error creating output directory: %v", err)
		return output
	}

	filePath := filepath.Join(outputDir, fmt.Sprintf("%s.log", resource.Name))
	file, err := os.Create(filePath)
	if err != nil {
		output.Stderr = fmt.Sprintf("error creating file %s: %v", filePath, err)
		return output
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	_, err = writer.WriteString(commandLog)
	if err != nil {
		output.Stderr = fmt.Sprintf("error writing to file %s: %v", filePath, err)
		return output
	}

	getCmd := exec.Command(command, cmdArgs...)
	stdoutPipe, err := getCmd.StdoutPipe()
	if err != nil {
		output.Stderr = fmt.Sprintf("error creating stdout pipe: %v", err)
		return output
	}
	stderrPipe, err := getCmd.StderrPipe()
	if err != nil {
		output.Stderr = fmt.Sprintf("error creating stderr pipe: %v", err)
		return output
	}

	err = getCmd.Start()
	if err != nil {
		output.Stderr = fmt.Sprintf("error starting command: %v", err)
		return output
	}

	_, err = io.Copy(writer, stdoutPipe)
	if err != nil {
		output.Stderr = fmt.Sprintf("error copying stdout to file: %v", err)
		return output
	}

	_, err = io.Copy(writer, stderrPipe)
	if err != nil {
		output.Stderr = fmt.Sprintf("error copying stderr to file: %v", err)
		return output
	}

	err = getCmd.Wait()
	if err != nil {
		output.Stderr = fmt.Sprintf("error running kubectl get command for resource %s: %v", resource.Name, err)
		return output
	}

	fmt.Printf("Writing %s\n", filePath)

	return output
}

func concatenateLogs(outputDir string) error {
	logFilePath := filepath.Join(outputDir, "log.txt")
	logFile, err := os.Create(logFilePath)
	if err != nil {
		return fmt.Errorf("error creating log file %s: %v", logFilePath, err)
	}
	defer logFile.Close()

	writer := bufio.NewWriter(logFile)
	defer writer.Flush()

	err = filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if path != logFilePath {
				file, err := os.Open(path)
				if err != nil {
					return fmt.Errorf("error opening file %s: %v", path, err)
				}
				defer file.Close()

				_, err = io.Copy(writer, file)
				if err != nil {
					return fmt.Errorf("error copying file contents to log file: %v", err)
				}

				_, err = writer.Write([]byte("\n\n"))
				if err != nil {
					return fmt.Errorf("error appending newline: %v", err)
				}

			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking through directory %s: %v", outputDir, err)
	}

	fmt.Printf("Logs concatenated to file %s\n", logFilePath)

	return nil
}
