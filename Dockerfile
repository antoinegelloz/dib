FROM golang:1.23

# Install graphviz which contains the dot executable
# --no-install-recommends helps keep the image size down
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive && \
    apt-get install -y --no-install-recommends graphviz && \
    rm -rf /var/lib/apt/lists/*

# Running as non-root is a security best practice.
# These ARGs are commonly used by VS Code Dev Containers features
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=$USER_UID

# Create the user and group
RUN groupadd --gid $USER_GID $USERNAME && useradd -s /bin/bash --uid $USER_UID --gid $USER_GID -m $USERNAME
# Give the user ownership of the home directory created by useradd
RUN chown $USERNAME:$USERNAME /home/$USERNAME

# Set the working directory inside the container
# This is where your project code will be copied
WORKDIR /app

# Copy your project files into the container
# The first '.' is the source (your project root), the second '.' is the destination (/app)
COPY . .

# Give the non-root user ownership of the working directory and copied files
RUN chown -R $USERNAME:$USERNAME /app

# Switch to the non-root user for subsequent instructions and when the container runs
USER $USERNAME
