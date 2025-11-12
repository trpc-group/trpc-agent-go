import sys

def fib(n: int):
    a, b = 0, 1
    for _ in range(n):
        print(a)
        a, b = b, a + b

if __name__ == "__main__":
    n = 10
    if len(sys.argv) > 1:
        try:
            n = int(sys.argv[1])
        except Exception:
            n = 10
    fib(n)

