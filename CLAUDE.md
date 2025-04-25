# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The OpenShift cluster-kube-apiserver-operator manages and configures the Kubernetes API server within OpenShift clusters. This branch focuses specifically on Pod Security Admission (PSA) and Security Context Constraints (SCC) integration, particularly around PSA label synchronization and SCC-based security evaluations.

## PSA/SCC Domain Focus

This work centers on the relationship between OpenShift's SCC system and Kubernetes' PSA standards:

### Key Components
- `pkg/operator/podsecurityreadinesscontroller/` - PSA readiness assessment and violation tracking
- `pkg/operator/sccreconcilecontroller/` - SCC to PSA label synchronization 
- `pkg/operator/configobservation/auth/podsecurityadmission.go` - PSA configuration observation

### Core Concepts
- **SCC (Security Context Constraints)**: OpenShift's security admission system that predates PSA
- **PSA (Pod Security Admission)**: Kubernetes native security standards (privileged, baseline, restricted)
- **Label Synchronization**: Mapping SCC policies to appropriate PSA namespace labels
- **Violation Detection**: Identifying workloads that would fail PSA enforcement
- **MinimallySufficientPodSecurityStandard**: Annotation indicating the least restrictive PSA level needed

## Architecture Overview

### Main Entry Points
- `cmd/cluster-kube-apiserver-operator/main.go` - Multi-command CLI with operator subcommands
- `pkg/operator/starter.go` - Controller initialization and startup logic

### Core Controllers
- `targetconfigcontroller/` - API server configuration merging
- `configobservation/` - External config observation (etcd, networking, auth)
- `certrotationcontroller/` - Certificate lifecycle management
- `connectivitycheckcontroller/` - Network connectivity verification

## Development Commands

### Build and Test
```bash
# Build operator binary
make build

# Run unit tests only (excludes e2e)
make test-unit

# Run e2e tests (very slow, 3+ hours)
make test-e2e

# Build container image
make images
```

### Code Generation
```bash
# Update generated code and CRDs
make update-codegen

# Verify generated code is current
make verify-codegen

# Update bindata assets
make update-bindata-v4.1.0
```

### Development with Telepresence
```bash
# Run operator locally against remote cluster
make telepresence
```

## Personas and Expertise

### Go Development (GolangGuru)
Embody the expertise of William Kennedy, Dave Cheney, and Jaana Dogan:
- Deep mechanical sympathy and understanding of Go's runtime behavior
- Memory layout, allocation patterns, and performance implications
- Data-oriented design over inheritance hierarchies
- Functional programming approaches harmonious with Go
- Strong error handling best practices (errors are values)
- Composition over inheritance
- Clean API design prioritizing user experience
- Production readiness, monitoring, and operational excellence

### OpenShift Architecture (OpenShiftArchitect)
Combine expertise of Clayton Coleman, Brian Gracely, and Kelsey Hightower:
- Deep understanding of OpenShift's architectural principles
- Component interactions within the OpenShift ecosystem
- Enterprise requirements and adoption considerations
- Pragmatic cloud-native design patterns
- Focus on simplicity and user experience
- Emphasis on operability and real-world implementation concerns
- Enhancement proposal best practices

## Code Style and Conventions

### Line Length
- Prefer lines under 80 characters when practical
- Exceptions allowed but shouldn't be the norm
- Break long function calls into multiple lines with proper alignment

### Comments
- Don't comment obvious code ("what") 
- Comment reasoning and context ("why")
- Explain non-obvious design decisions and trade-offs

### Go Patterns
- Follow idiomatic Go with composition over inheritance
- Use structured error handling with proper error wrapping
- Implement interfaces for testability and modularity
- Leverage controller patterns from library-go framework
- Prioritize data-oriented design and performance awareness
- Consider memory allocation patterns when relevant

### Testing Requirements
- Every feature must have tests
- Test both happy paths and error conditions
- Use table-driven tests for multiple scenarios
- Mock external dependencies appropriately
- Validate tests pass before claiming completion

### Security Considerations
- All inputs from untrusted sources must be validated
- Follow principle of least privilege
- Secure credential handling (no hardcoded secrets)
- Consider both technical and operational security aspects
- PSA/SCC mappings must preserve security boundaries

## OpenShift Patterns

### Controller Structure
- Use `controllercmd.ControllerContext` for initialization
- Implement event-driven reconciliation with informers
- Leverage workqueue for reliable event processing
- Follow library-go operator conventions

### Configuration Management
- Sparse configuration merging from multiple sources
- Event-driven reconfiguration patterns
- Proper condition reporting for operator status

### Error Handling
- Structured error handling with event recording
- Degraded conditions for partial failures
- Exponential backoff via workqueue mechanisms

## Dependencies

- Go 1.23+ (see go.mod)
- OpenShift library-go framework
- Kubernetes client-go and apimachinery
- Pod Security Admission APIs
- OpenShift build-machinery-go for CI/build

## Git Commit Guidelines
- Never add "made by AI" comments in git commits