`tone.mp3` is half a second of a 440 Hz sine, mono, 22.05 kHz, made with:

    ffmpeg -f lavfi -i "sine=frequency=440:duration=0.5:sample_rate=22050" -ac 1 -b:a 32k tone.mp3
