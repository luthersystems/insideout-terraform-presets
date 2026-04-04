package compose

import (
	"strings"
	"testing"
)

func TestComposeEmpty(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{},
	}
	files, err := Compose(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["/main.tf"]; !ok {
		t.Error("expected /main.tf in output")
	}
	if _, ok := files["/variables.tf"]; !ok {
		t.Error("expected /variables.tf in output")
	}
}

func TestComposeNilSpec(t *testing.T) {
	_, err := Compose(nil)
	if err == nil {
		t.Fatal("expected error for nil spec")
	}
}

func TestComposeDuplicateNames(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{
			{Name: "vpc", PresetPath: "aws/vpc"},
			{Name: "vpc", PresetPath: "aws/vpc"},
		},
	}
	_, err := Compose(spec)
	if err == nil {
		t.Fatal("expected error for duplicate module names")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected 'duplicate' in error, got: %s", err)
	}
}

func TestComposeInvalidName(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{
			{Name: "123-bad", PresetPath: "aws/vpc"},
		},
	}
	_, err := Compose(spec)
	if err == nil {
		t.Fatal("expected error for invalid module name")
	}
}

func TestComposeSingleModule(t *testing.T) {
	spec := &StackSpec{
		TerraformVersion: "1.7.5",
		Modules: []ModuleSpec{
			{
				Name:       "ecs",
				PresetPath: "aws/ecs",
				Values: map[string]any{
					"project":     "myapp",
					"region":      "us-west-2",
					"environment": "prod",
					"vpc_id":      "vpc-123",
				},
			},
		},
	}

	files, err := Compose(spec)
	if err != nil {
		t.Fatal(err)
	}

	// Check main.tf contains module block
	mainTF := string(files["/main.tf"])
	if !strings.Contains(mainTF, `module "ecs"`) {
		t.Error("main.tf should contain module \"ecs\" block")
	}
	if !strings.Contains(mainTF, "modules/ecs") {
		t.Error("main.tf should reference modules/ecs as source")
	}

	// Check variables.tf has namespaced variables
	varsTF := string(files["/variables.tf"])
	if !strings.Contains(varsTF, "ecs_project") {
		t.Error("variables.tf should contain ecs_project")
	}
	if !strings.Contains(varsTF, "ecs_vpc_id") {
		t.Error("variables.tf should contain ecs_vpc_id")
	}

	// Check auto.tfvars
	tfvars := string(files["/ecs.auto.tfvars"])
	if !strings.Contains(tfvars, "ecs_project") {
		t.Error("ecs.auto.tfvars should contain ecs_project")
	}

	// Check .terraform-version
	if string(files["/.terraform-version"]) != "1.7.5\n" {
		t.Error("expected .terraform-version to be 1.7.5")
	}

	// Check preset files were rebased
	if _, ok := files["/modules/ecs/main.tf"]; !ok {
		t.Error("expected /modules/ecs/main.tf in output")
	}

	// Check outputs.tf has namespaced outputs
	outTF := string(files["/outputs.tf"])
	if !strings.Contains(outTF, "ecs_cluster_name") {
		t.Error("outputs.tf should re-export ecs_cluster_name")
	}
}

func TestComposeMultipleInstancesSamePreset(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{
			{
				Name:       "vpc",
				PresetPath: "aws/vpc",
				Values: map[string]any{
					"project":     "myapp",
					"region":      "us-west-2",
					"environment": "prod",
				},
			},
			{
				Name:       "lambda_api",
				PresetPath: "aws/lambda",
				Values: map[string]any{
					"project":     "myapp",
					"region":      "us-west-2",
					"environment": "prod",
					"runtime":     "nodejs20.x",
				},
				Wiring: map[string]string{
					"vpc_id":     "module.vpc.vpc_id",
					"subnet_ids": "module.vpc.private_subnet_ids",
				},
			},
			{
				Name:       "lambda_worker",
				PresetPath: "aws/lambda",
				Values: map[string]any{
					"project":     "myapp",
					"region":      "us-west-2",
					"environment": "prod",
					"runtime":     "python3.12",
				},
				Wiring: map[string]string{
					"vpc_id":     "module.vpc.vpc_id",
					"subnet_ids": "module.vpc.private_subnet_ids",
				},
			},
		},
	}

	files, err := Compose(spec)
	if err != nil {
		t.Fatal(err)
	}

	mainTF := string(files["/main.tf"])

	// Both lambda modules should exist
	if !strings.Contains(mainTF, `module "lambda_api"`) {
		t.Error("main.tf should contain module \"lambda_api\"")
	}
	if !strings.Contains(mainTF, `module "lambda_worker"`) {
		t.Error("main.tf should contain module \"lambda_worker\"")
	}

	// Check namespaced variables don't collide
	varsTF := string(files["/variables.tf"])
	if !strings.Contains(varsTF, "lambda_api_runtime") {
		t.Error("variables.tf should contain lambda_api_runtime")
	}
	if !strings.Contains(varsTF, "lambda_worker_runtime") {
		t.Error("variables.tf should contain lambda_worker_runtime")
	}

	// Each should have its own .auto.tfvars
	apiTFVars := string(files["/lambda_api.auto.tfvars"])
	if !strings.Contains(apiTFVars, "lambda_api_runtime") {
		t.Error("lambda_api.auto.tfvars should contain lambda_api_runtime")
	}
	workerTFVars := string(files["/lambda_worker.auto.tfvars"])
	if !strings.Contains(workerTFVars, "lambda_worker_runtime") {
		t.Error("lambda_worker.auto.tfvars should contain lambda_worker_runtime")
	}

	// Wiring should appear as raw HCL in main.tf
	if !strings.Contains(mainTF, "module.vpc.vpc_id") {
		t.Error("main.tf should contain wired vpc_id reference")
	}

	// Each should have its own preset directory
	if _, ok := files["/modules/lambda_api/main.tf"]; !ok {
		t.Error("expected /modules/lambda_api/main.tf")
	}
	if _, ok := files["/modules/lambda_worker/main.tf"]; !ok {
		t.Error("expected /modules/lambda_worker/main.tf")
	}
}

func TestComposeWithWiring(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{
			{
				Name:       "vpc",
				PresetPath: "aws/vpc",
				Values: map[string]any{
					"project":     "myapp",
					"region":      "us-west-2",
					"environment": "prod",
				},
			},
			{
				Name:       "alb",
				PresetPath: "aws/alb",
				Values: map[string]any{
					"project":     "myapp",
					"region":      "us-west-2",
					"environment": "prod",
				},
				Wiring: map[string]string{
					"vpc_id":            "module.vpc.vpc_id",
					"public_subnet_ids": "module.vpc.public_subnet_ids",
				},
			},
		},
	}

	files, err := Compose(spec)
	if err != nil {
		t.Fatal(err)
	}

	mainTF := string(files["/main.tf"])

	// Wired vars should NOT appear in variables.tf
	varsTF := string(files["/variables.tf"])
	if strings.Contains(varsTF, "alb_vpc_id") {
		t.Error("wired variable alb_vpc_id should NOT appear in variables.tf")
	}

	// Wired vars should appear as raw refs in main.tf
	if !strings.Contains(mainTF, "module.vpc.vpc_id") {
		t.Error("main.tf should contain wired module.vpc.vpc_id")
	}
}

func TestComposeWithProviders(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{},
		Providers: &ProvidersSpec{
			Cloud:  "aws",
			Region: "us-west-2",
		},
	}

	files, err := Compose(spec)
	if err != nil {
		t.Fatal(err)
	}

	provTF := string(files["/providers.tf"])
	if !strings.Contains(provTF, "us-west-2") {
		t.Error("providers.tf should contain region us-west-2")
	}
	if !strings.Contains(provTF, "hashicorp/aws") {
		t.Error("providers.tf should reference hashicorp/aws")
	}
}

func TestComposeGCPProviders(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{},
		Providers: &ProvidersSpec{
			Cloud:  "gcp",
			Region: "us-central1",
		},
	}

	files, err := Compose(spec)
	if err != nil {
		t.Fatal(err)
	}

	provTF := string(files["/providers.tf"])
	if !strings.Contains(provTF, "hashicorp/google") {
		t.Error("providers.tf should reference hashicorp/google")
	}
}

func TestComposeExcludeOutputs(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{
			{
				Name:           "vpc",
				PresetPath:     "aws/vpc",
				ExcludeOutputs: true,
				Values: map[string]any{
					"project":     "myapp",
					"region":      "us-west-2",
					"environment": "prod",
				},
			},
		},
	}

	files, err := Compose(spec)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := files["/outputs.tf"]; ok {
		t.Error("outputs.tf should not be emitted when all modules exclude outputs")
	}
}

func TestComposeCustomSourcePath(t *testing.T) {
	spec := &StackSpec{
		Modules: []ModuleSpec{
			{
				Name:       "vpc",
				PresetPath: "aws/vpc",
				SourcePath: "custom/vpc_module",
				Values: map[string]any{
					"project":     "myapp",
					"region":      "us-west-2",
					"environment": "prod",
				},
			},
		},
	}

	files, err := Compose(spec)
	if err != nil {
		t.Fatal(err)
	}

	mainTF := string(files["/main.tf"])
	if !strings.Contains(mainTF, "custom/vpc_module") {
		t.Error("main.tf should use custom source path")
	}

	if _, ok := files["/custom/vpc_module/main.tf"]; !ok {
		t.Error("expected preset files under /custom/vpc_module/")
	}
}
