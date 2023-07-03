package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Resource struct {
	Name       string `json:"name"`
	ShortNames string `json:"shortNames"`
	APIVersion string `json:"apiVersion"`
	Namespaced bool   `json:"namespaced"`
	Kind       string `json:"kind"`
}

func main() {
	cmd := exec.Command("kubectl", "api-resources")
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

	err = processResources(resourceList)
	if err != nil {
		fmt.Printf("Error processing resources: %v\n", err)
		return
	}
}

func processResources(resources []Resource) error {
	fmt.Printf("Parsed resources:\n")
	for _, resource := range resources {
		fmt.Printf("%#v\n", resource)
		err := processResource(resource)
		if err != nil {
			fmt.Printf("Error processing resource %s: %v\n", resource.Name, err)
		}
	}
	return nil
}

func processResource(resource Resource) error {
	command := "kubectl"
	cmdArgs := []string{"get", "--all-namespaces", resource.Name}
	joined := strings.Join(cmdArgs, " ")
	commandLog := fmt.Sprintf("Running command: %s %s\n", command, joined)

	outDir := "resources"

	// Create data directory if it doesn't exist
	err := os.MkdirAll(outDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("error creating data directory: %v", err)
	}

	// Write command log and output to file
	filePath := filepath.Join(outDir, resource.Name)
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating file %s: %v", filePath, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	_, err = writer.WriteString(commandLog)
	if err != nil {
		return fmt.Errorf("error writing to file %s: %v", filePath, err)
	}

	// Create a pipe for capturing the command's stdout and stderr
	getCmd := exec.Command(command, cmdArgs...)
	stdoutPipe, err := getCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("error creating stdout pipe: %v", err)
	}
	stderrPipe, err := getCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("error creating stderr pipe: %v", err)
	}

	// Start the command
	err = getCmd.Start()
	if err != nil {
		return fmt.Errorf("error starting command: %v", err)
	}

	// Copy stdout to the log file
	_, err = io.Copy(writer, stdoutPipe)
	if err != nil {
		return fmt.Errorf("error copying stdout to file: %v", err)
	}

	// Copy stderr to the log file
	_, err = io.Copy(writer, stderrPipe)
	if err != nil {
		return fmt.Errorf("error copying stderr to file: %v", err)
	}

	// Wait for the command to finish
	err = getCmd.Wait()
	if err != nil {
		return fmt.Errorf("error running kubectl get command for resource %s: %v", resource.Name, err)
	}

	fmt.Printf("Output written to file %s\n", filePath)

	return nil
}
