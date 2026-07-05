.PHONY: run run-wsl build build-linux setup setup-win clean kill

## run: Build and start the study app on :8080
run:
	go run .

## run-wsl: Cross-compila p/ Linux e roda DENTRO do WSL (client-go nativo, acesso direto ao minikube)
run-wsl: build-linux
	wsl.exe -- bash -c 'cd /mnt/c/desenv/estudo-app && ./estudo-app-linux'

## build: Compile to a binary
build:
	go build -o k8s-study-lab .

## build-linux: Cross-compila o binário Linux (autossuficiente, assets embutidos) p/ rodar no WSL
build-linux:
	GOOS=linux GOARCH=amd64 go build -o estudo-app-linux .

## setup: Install kubectl + minikube and start cluster (Linux/macOS)
setup:
	@bash scripts/setup.sh

## setup-win: Install kubectl + minikube and start cluster (Windows PowerShell)
setup-win:
	powershell -ExecutionPolicy Bypass -File scripts\setup.ps1

## clean: Remove compiled binary
clean:
	rm -f k8s-study-lab k8s-study-lab.exe

## kill: Stop any process using port 8080
kill:
	@echo "Parando processo na porta 8080..."
	@if command -v lsof >/dev/null 2>&1; then \
		PID=$$(lsof -ti:8080) && [ -n "$$PID" ] && kill -9 $$PID && echo "PID $$PID finalizado" || echo "Nenhum processo encontrado"; \
	else \
		echo "Use: netstat -ano | findstr 8080  (Windows)"; \
	fi

## cluster-start: Start minikube cluster
cluster-start:
	minikube start --driver=docker --cpus=2 --memory=2048

## cluster-stop: Stop minikube cluster
cluster-stop:
	minikube stop

## cluster-status: Check cluster status
cluster-status:
	kubectl cluster-info
	kubectl get nodes

help:
	@echo ""
	@echo "K8s Study Lab — Comandos disponíveis:"
	@echo ""
	@grep -E '^##' Makefile | sed 's/## /  make /'
	@echo ""
