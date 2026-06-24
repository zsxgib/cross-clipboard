import sys
content = sys.argv[1]
with open(sys.argv[2], 'wb') as f:
    f.write(content.encode('utf-8'))
print(f"wrote {len(content)} bytes to {sys.argv[2]}")
