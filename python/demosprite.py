import time
import random
import montage

for n in range(10):
    x = random.random()
    y = random.random()
    z = random.random() / 4.0
    montage.SendSpriteEvent("0",x,y,z)
    time.sleep(0.1)
