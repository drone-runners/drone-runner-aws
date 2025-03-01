FROM golang:1.18

# Set the working directory inside the container
WORKDIR /src

# Copy the Go source code into the container
COPY . .

# Install build dependencies (if needed)
RUN apt-get update && apt-get install -y \
    gcc \
    make \
    && rm -rf /var/lib/apt/lists/*

# Build the Go binary with the desired flags
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags "-extldflags \"-static\"" -o release/linux/amd64/drone-runner-aws-linux-amd64

# Set the entry point (optional, depends on your app)
# CMD ["/path/to/your/binary"]

