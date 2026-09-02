.PHONY: test test-race vet web-test web-test-syntax run build \
	android-wrapper-verify android-env android-test android-lint android-assemble android-check ci

test:
	go test ./...
	$(MAKE) web-test

web-test:
	npm --prefix frontend test

web-test-syntax:
	npm --prefix frontend run test:syntax

test-race:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/checknetwork-api

build:
	go build -trimpath -o bin/checknetwork-api ./cmd/checknetwork-api

android-wrapper-verify:
	./scripts/verify-android-wrapper.sh

android-env:
	./scripts/verify-android-env.sh

android-test: android-wrapper-verify android-env
	rm -rf android/app/build/test-results/testDebugUnitTest android/app/build/test-results/testReleaseUnitTest
	cd android && ./gradlew --no-daemon --console=plain :app:testDebugUnitTest :app:testReleaseUnitTest
	./scripts/assert-android-test-results.sh \
		android/app/build/test-results/testDebugUnitTest \
		android/app/build/test-results/testReleaseUnitTest

android-lint: android-wrapper-verify android-env
	cd android && ./gradlew --no-daemon --console=plain :app:lintDebug :app:lintRelease

android-assemble: android-wrapper-verify android-env
	cd android && ./gradlew --no-daemon --console=plain :app:assembleDebug :app:assembleRelease

android-check: android-wrapper-verify android-env
	$(MAKE) android-test
	$(MAKE) android-lint
	$(MAKE) android-assemble

ci:
	$(MAKE) test
	$(MAKE) android-check
