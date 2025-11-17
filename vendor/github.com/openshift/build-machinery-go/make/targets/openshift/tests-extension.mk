# OpenShift Tests Extension targets
#
# This file provides common targets for building and running OpenShift test extensions
# across all operator repos that use the openshift-tests-extension framework.
#
# Usage in main Makefile:
#   include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
#       targets/openshift/tests-extension.mk \
#   )
#
#   TESTS_EXT_BINARY := my-operator-tests-ext
#   TESTS_EXT_DIR := ./cmd/my-operator-tests
#   TESTS_EXT_OUTPUT_DIR := ./cmd/my-operator-tests  # Optional, defaults to TESTS_EXT_DIR
#
# Required variables:
#   TESTS_EXT_BINARY - Name of the test extension binary
#   TESTS_EXT_DIR - Directory containing test extension code (e.g., ./cmd/my-operator-tests)
#
# Optional variables:
#   TESTS_EXT_OUTPUT_DIR - Output directory for the binary (defaults to TESTS_EXT_DIR)

# Default values
TESTS_EXT_OUTPUT_DIR ?= $(TESTS_EXT_DIR)

# -------------------------------------------------------------------
# Ensure test binary has correct name and location
# -------------------------------------------------------------------
.PHONY: tests-ext-build
tests-ext-build: build
	@mkdir -p $(TESTS_EXT_OUTPUT_DIR)
	@if [ -f $(TESTS_EXT_BINARY) ] && [ ! -f $(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY) ]; then \
		mv $(TESTS_EXT_BINARY) $(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY); \
	elif [ -f $(shell basename $(TESTS_EXT_DIR)) ] && [ ! -f $(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY) ]; then \
		mv $(shell basename $(TESTS_EXT_DIR)) $(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY); \
	fi

# -------------------------------------------------------------------
# Run test suite
# -------------------------------------------------------------------
.PHONY: run-suite
run-suite: tests-ext-build
	@if [ -z "$(SUITE)" ]; then \
		echo "Error: SUITE variable is required. Usage: make run-suite SUITE=<suite-name> [JUNIT_DIR=<dir>]"; \
		exit 1; \
	fi
	@JUNIT_ARG=""; \
	if [ -n "$(JUNIT_DIR)" ]; then \
		mkdir -p $(JUNIT_DIR); \
		JUNIT_ARG="--junit-path=$(JUNIT_DIR)/junit.xml"; \
	fi; \
	$(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY) run-suite $(SUITE) $$JUNIT_ARG

# -------------------------------------------------------------------
# Clean test extension binaries
# -------------------------------------------------------------------
.PHONY: tests-ext-clean
tests-ext-clean:
	rm -f $(TESTS_EXT_OUTPUT_DIR)/$(TESTS_EXT_BINARY)

# Hook into standard clean target
clean: tests-ext-clean
