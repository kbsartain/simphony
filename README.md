# Simphony

An implementation of the [OpenAI Symphony](https://github.com/openai/symphony/blob/main/SPEC.md) specification.

## Quick Start

```bash
cd simphony
go mod tidy
go build ./cmd/simphony
./simphony -workflow ./WORKFLOW.md
```

## Project Structure

- `cmd/simphony/` — Entry point
- `internal/` — Core implementation packages
- `pkg/api/` — Public types and interfaces
- `dashboard/` — Optional TypeScript/React dashboard

See `AGENTS.md` for detailed development guidance.
