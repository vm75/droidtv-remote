FROM python:3.11-slim

WORKDIR /app

# Install dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Create data directory
RUN mkdir -p data

# Copy application files
COPY server.py .
# We don't copy certificates because they will be generated if missing,
# but it's better to mount them as a volume to persist pairing.
COPY static/ ./static/

# Expose the server port
EXPOSE 7503

# Run the server
CMD ["python", "server.py"]
