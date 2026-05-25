import urllib.request, sys

try:
    urllib.request.urlopen("http://localhost:8000/health", timeout=2)
except Exception:
    sys.exit(1)
