FROM redhat/ubi9-minimal

ARG GO_VERSION=1.25.2

# Update OS packages
RUN microdnf update -y && \
    microdnf install -y tar gzip gcc glibc-devel && \
    microdnf clean all

# Install go
RUN curl -LO https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz && \
    rm go${GO_VERSION}.linux-amd64.tar.gz && \
    ln -s /usr/local/go/bin/go /usr/local/bin/go && \
    ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt

# Set Go environment variables
ENV GOPATH=/go
ENV PATH=$GOPATH/bin:/usr/local/go/bin:$PATH

# Create app directory
WORKDIR /app

# Download Go modules
COPY go.mod .
COPY go.sum .
RUN go mod download

# Copy source code
COPY main.go .
COPY cmd ./cmd
COPY internal ./internal

# Define volume for output
VOLUME /dist

CMD ["/bin/sh", "-c", "CGO_ENABLED=1 GOOS=linux go build -o /dist/filebackup main.go"]
