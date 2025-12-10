# Contributing to Air-Mixer

Thank you for your interest in contributing to Air-Mixer! This document provides guidelines and instructions for contributing.

## Code of Conduct

- Be respectful and inclusive
- Welcome newcomers and help them learn
- Focus on constructive feedback
- Respect different viewpoints and experiences

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/air-mixer.git`
3. Create a branch: `git checkout -b feature/your-feature-name`
4. Make your changes
5. Test your changes
6. Commit your changes: `git commit -m "Add feature: description"`
7. Push to your fork: `git push origin feature/your-feature-name`
8. Open a Pull Request

## Development Setup

### Prerequisites

- Go 1.21 or later
- Make (optional, for using Makefile)
- Docker (for testing Docker builds)

### Building

```bash
# Install dependencies
go mod download

# Build the binary
go build -o airmixer ./cmd/airmixer

# Run tests
go test ./...

# Run with race detector
go test -race ./...
```

### Project Structure

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed architecture documentation.

Key directories:
- `cmd/airmixer/` - Main application entry point
- `internal/` - Internal packages (not for external use)
- `webui/` - Web UI source and assets
- `migrations/` - Database migrations

## Coding Standards

### Go Style

- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines
- Use `gofmt` to format code
- Run `golint` and fix warnings
- Keep functions focused and small
- Write clear, descriptive names
- Add comments for exported functions and types

### Code Organization

- Keep related code together
- Use interfaces for abstraction
- Avoid circular dependencies
- Prefer composition over inheritance

### Error Handling

- Always handle errors explicitly
- Return errors, don't panic (except in truly unrecoverable situations)
- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- Use `errors.Is()` and `errors.As()` for error checking

### Testing

- Write tests for new features
- Aim for good test coverage
- Use table-driven tests where appropriate
- Test both success and error cases
- Mock external dependencies

Example:
```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {
            name:    "valid input",
            input:    "test",
            want:     "result",
            wantErr:  false,
        },
        // ... more test cases
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := MyFunction(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("MyFunction() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if got != tt.want {
                t.Errorf("MyFunction() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

## Commit Messages

Follow the [Conventional Commits](https://www.conventionalcommits.org/) format:

```
<type>(<scope>): <subject>

<body>

<footer>
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes (formatting, etc.)
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `chore`: Maintenance tasks

Examples:
```
feat(router): add support for many-to-many routing

Implemented routing engine that can mix multiple sources
and send to multiple destinations simultaneously.

Closes #123
```

```
fix(airplay): handle connection timeout gracefully

Previously, timeouts would cause a panic. Now they are
handled with proper error messages and cleanup.
```

## Pull Request Process

1. **Update Documentation**: Update README, ARCHITECTURE.md, or other docs if needed
2. **Add Tests**: Ensure new code has appropriate test coverage
3. **Update CHANGELOG**: Add entry describing your changes (if applicable)
4. **Check CI**: Ensure all CI checks pass
5. **Request Review**: Request review from maintainers

### PR Checklist

- [ ] Code follows project style guidelines
- [ ] Self-review completed
- [ ] Comments added for complex code
- [ ] Documentation updated
- [ ] Tests added/updated
- [ ] All tests pass
- [ ] No new warnings introduced
- [ ] CHANGELOG updated (if applicable)

## Reporting Issues

When reporting bugs or requesting features:

1. Check existing issues to avoid duplicates
2. Use clear, descriptive titles
3. Provide detailed information:
   - Steps to reproduce (for bugs)
   - Expected vs actual behavior
   - Environment details (OS, Go version, etc.)
   - Relevant logs or error messages
4. Include code examples if applicable

## Feature Requests

For feature requests:
- Describe the use case
- Explain why it would be useful
- Suggest implementation approach (if you have ideas)
- Consider contributing the feature yourself!

## Questions?

- Open an issue for discussion
- Check existing documentation
- Ask in discussions (if enabled)

Thank you for contributing to Air-Mixer! 🎵

