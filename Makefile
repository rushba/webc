MODULES := stack consumer lambda producer tools/cleanup

.PHONY: build test deploy clean lint fmt

build:
	cd lambda && ./build.sh

test:
	@for dir in $(MODULES); do \
		if ls $$dir/*_test.go >/dev/null 2>&1; then \
			echo "Testing $$dir..." && (cd $$dir && go test ./...) || exit 1; \
		fi; \
	done

deploy: build
	cd cdk && cdk deploy

clean:
	cd tools/cleanup && go run . --all

lint:
	@for dir in $(MODULES); do \
		echo "Linting $$dir..." && (cd $$dir && golangci-lint run ./...) || exit 1; \
	done

fmt:
	@for dir in $(MODULES); do \
		echo "Formatting $$dir..." && (cd $$dir && gofmt -w .) || exit 1; \
	done
