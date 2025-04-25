# Programming

## Paradigms

- Code should be idempotent if possible. E.g. if we need to add an object to override in the clusterversion manifest, we should check if that override already is done, before applying it. If we want to modify a deployment manifest with an image, we should check if that images is already modified.

## Feature development

- Every feature you create should have meaningful tests.
- Review every features based on the persona file for the specific programming language.

## Debugging

- If you create debugging files or code, you must remove them after you resolved the issue and before claiming to be done.

## Comments

- Don't add comments that explain the "WHAT", which should be obvious from the code.
- Add comments that explain the "WHY", if that isn't obvious.
- If a request is made to explain a certain code segment, adding comments there makes sense.
- This is an example how you must not comment ever, it describes the obvious:
    ```go
    // Close closes the database connection.
    func (s *Store) Close() error {
        return s.db.Close()
    }
    ```
- This is an example how you should do a comment, it describes the reasoning behind it:
    ```go
    // Close releases database resources and prevents connection limit exhaustion.
    func (s *Store) Close() error {
        return s.db.Close()
    }
    ```

## Line Length

- Lines shouldn't exceed 80 columns in most cases.
- Exceptions are fine, but they shouldn't be the rule.
- Bad:
  ```go
  id, err = store.CreateUser(api.User{Login: "octocat",HTMLURL: "https://github.com/octocat", IsOrg: false, })
  ```
- Good:
  ```go
  id, err = store.CreateUser(api.User{
      Login:   "octocat",
      HTMLURL: "https://github.com/octocat",
      IsOrg:   false,
  }
  ```

## Abstractions

- **YAGNI (You Aren't Gonna Need It)**: Don't add abstractions until you actually need them. Wait for 3+ identical use cases before creating helper functions.
- **Don't Hide Simple Things Behind Functions**: If the abstraction is longer to type than the original code and doesn't reduce significant duplication, it's probably unnecessary.
- **Prefer Obvious Over Clever**: When someone reads your code, they should immediately understand what it does without having to look up function definitions.
- Example of unnecessary abstraction:
    ```go
    // Bad - forces reader to look up function definition
    AssertError(t, err, false, "should not error")
    
    // Good - immediately obvious
    if err != nil {
        t.Errorf("should not error: %v", err)
    }
    ```
- **Exception**: Create abstractions when they provide clear value through meaningful reduction of complexity or duplication, not just for the sake of "being DRY".