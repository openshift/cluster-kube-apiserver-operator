# TODO - PSA/SCC User Violation Detection Optimization

## Completed ✅

### Core Implementation
- [x] Optimize pod listing in isUserViolation using client-side filtering for user-annotated pods
- [x] Move PSA evaluator creation outside the namespace loop to avoid recreating it repeatedly
- [x] Add early return if no user-annotated pods exist in namespace (optimization)
- [x] Add logging to distinguish between user vs service account violations for debugging
- [x] Verify field selector syntax and implement fallback approach
- [x] Consider caching PSA evaluator as controller field instead of recreating
- [x] Replace panic with proper error handling in PSA evaluator creation

### Code Quality & Architecture
- [x] Consolidate duplicate namespace evaluation logic between `shouldCheckForUserSCC` and `addViolation`
  - Moved namespace categorization logic into `addViolation` method
  - Simplified `shouldCheckForUserSCC` to just check if it's a customer namespace
  - Eliminated duplicate run-level-zero, openshift, and disabled syncer checks
- [x] Fix double reporting issue by moving user violation check inside `addViolation`
  - Updated `addViolation` to accept `isUserViolation` parameter
  - Removed separate `addUserSCCViolation` method
  - User violations are now tracked within the consolidated logic, preventing double reporting
- [x] Use `violationReason` instead of custom `userSCCReason` for consistency
  - Removed custom `userSCCReason` constant
  - User SCC violations now use the same reason as other violations
  - Maintains consistency with existing condition patterns

### Testing & Validation
- [x] Add unit tests for the optimized isUserViolation method
- [x] Fix all linter errors (InvalidIfaceAssign, UnusedVar, UnusedImport, MissingFieldOrMethod, WrongAssignCount)
- [x] Fix test files to handle new method signature
- [x] Create test manifests and verification commands for manual testing

### Telemetry Integration
- [x] Add new condition type for user SCC violations for telemetry
  - Added `PodSecurityUserSCCViolationConditionsDetected` condition
  - Provides separate telemetry metrics for user vs service account violations
  - Enables OpenShift teams to understand PSA adoption blockers

## Summary
All optimization tasks completed successfully. The feature now:
- **Efficiently detects** user-based SCC violations vs service account violations
- **Provides separate telemetry** metrics via `PodSecurityUserSCCViolationConditionsDetected` condition
- **Eliminates duplicate logic** with consolidated namespace categorization
- **Prevents double reporting** with unified violation tracking
- **Includes comprehensive testing** and manual verification tools
- **Builds and passes all tests** with proper error handling

The implementation is production-ready and provides valuable telemetry data for OpenShift PSA adoption insights.