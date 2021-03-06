package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/awslabs/aws-sdk-go/aws"
	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/flynn/go-docopt"
	"github.com/flynn/flynn/installer"
)

func init() {
	validInstanceTypes := strings.Join(installer.ValidEC2InstanceTypes, ", ")
	defaultInstanceType := installer.ValidEC2InstanceTypes[0]

	register("install", runInstaller, fmt.Sprintf(`
usage: flynn install <target> [-n <instances>] [-t <instance-type>] [--aws-access-key-id <key-id>] [--aws-secret-access-key <secret>] [--aws-region <region>]

Targets:
	aws  creates a flynn cluster on EC2

Options:
  -n, --instances <instances> Number of instances to launch [default: 1]
  -t, --type <instance-type> Type of instances to launch (%s) [default: %s]
      --aws-access-key-id <key-id>  AWS access key ID. Defaults to $AWS_ACCESS_KEY_ID
      --aws-secret-access-key <secret> AWS access key secret. Defaults to $AWS_SECRET_ACCESS_KEY
      --aws-region <region> AWS region [default: us-east-1]

Examples:

	$ flynn install aws --aws-access-key-id=asdf --aws-secret-access-key=fdsa
`, validInstanceTypes, defaultInstanceType))
}

func runInstaller(args *docopt.Args) error {
	if args.String["<target>"] != "aws" {
		return errors.New("Invalid install target")
	}
	var creds aws.CredentialsProvider
	key := args.String["--aws-access-key-id"]
	secret := args.String["--aws-secret-access-key"]
	if key != "" && secret != "" {
		creds = aws.Creds(key, secret, "")
	} else {
		var err error
		creds, err = aws.EnvCreds()
		if err != nil {
			return err
		}
	}

	instanceType := args.String["--type"]

	region := args.String["--aws-region"]
	if region == "" {
		region = "us-east-1"
	}

	instances := 1
	if args.String["--instances"] != "" {
		var err error
		instances, err = strconv.Atoi(args.String["--instances"])
		if err != nil {
			return err
		}
	}

	stack := &installer.Stack{
		NumInstances: instances,
		InstanceType: instanceType,
		Region:       region,
		Creds:        creds,
		YesNoPrompt:  promptYesNo,
		PromptInput:  promptInput,
	}
	if err := stack.RunAWS(); err != nil {
		return err
	}

	exitCode := 0
	for {
		select {
		case event := <-stack.EventChan:
			fmt.Println(event.Description)
		case err := <-stack.ErrChan:
			fmt.Printf("Oops, something went wrong: %s\n", err.Error())
			exitCode = 1
		case <-stack.Done:
			os.Exit(exitCode)
		}
	}
	return nil
}

func promptInput(msg string) (result string) {
	fmt.Print(msg)
	fmt.Print(": ")
	for {
		var answer string
		fmt.Scanln(&answer)
		return answer
	}
}
