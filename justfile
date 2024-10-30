# run quality control checks
audit:
    go mod verify
    go vet ./...
    go run honnef.co/go/tools/cmd/staticcheck@latest ./...
    go run github.com/securego/gosec/v2/cmd/gosec@latest -exclude-generated -exclude-dir=examples  ./...
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...
    go test -vet=off -v

# run tests
test:
    go test -v

test-gen *ARGS:
    go run go.uber.org/mock/mockgen@latest {{ ARGS }}

# format code and tidy modfile
tidy:
    go fmt ./...
    go mod tidy -v
