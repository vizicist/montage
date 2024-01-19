import sys

import montage

if len(sys.argv) > 1:
    p = sys.argv[1]
else:
    p = "debug"
print(montage.ConfigValue(p))
