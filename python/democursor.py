import time
import random
import montage

# Send a cursor event at a random position
def randomcursor(downup):
    x = random.random()
    y = random.random()
    z = random.random() / 4.0
    montage.SendCursorEvent("0",downup,x,y,z)

# Send 10 random cursor down and then up events
for n in range(10):
    randomcursor("down")
    time.sleep(0.2)
    randomcursor("up")
    time.sleep(0.2)
