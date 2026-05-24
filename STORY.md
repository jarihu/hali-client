## The Life of a Pull

You open your terminal and type three words: `hali pull mistral`. Behind that simple command, an entire chain of events unfolds.

---

### 1. The Front Door

The program starts. It's called `bt`, and the first thing it does is look at your command and figure out where to send it. "Pull," it says to itself, "that means someone wants a model." It hands off to the pull handler, which is nothing more than a waiter taking an order — it holds no memory of previous visits, no knowledge of what's on disk. It just takes the order and starts figuring things out.

---

### 2. The Quick Check

Before doing anything expensive, the waiter checks its notepad. "Mistral," you said. Could that already be a model name? It tries to parse it into the four-part format it understands: `base:size:variant:quant` — something like `mistral:7b:instruct:q4_k_m`. Three colons, four parts. Your input has zero colons. That's not a model ID.

Still, it peeks into the pantry — a folder called `~/.hali/models/` — just in case something named "mistral" is already sitting there. It's empty. No shortcut this time. Time to go find this model for real.

---

### 3. Finding the Right Store

The waiter needs to know *which* model you want, and there might be dozens called "mistral." It asks itself: did you give it a full address with a slash, like `TheBloke/Mistral-7B-Instruct-v0.2-GGUF`? No? Then you must be searching.

It picks up the phone and calls Hugging Face, the giant library of AI models. "Show me models matching 'mistral,'" it asks, "GGUF format only, sorted by popularity." Hugging Face sends back a list: twenty repositories, ranked by how many people have downloaded them.

The waiter lays out the list on your screen with numbers, and asks you to pick one. You type a number. The choice is made: `mistralai/Mistral-7B-Instruct-v0.2-GGUF`.

---

### 4. Picking the Right File

One repo can hold many files. A 7-billion-parameter model might come in a dozen different quantizations — some tiny and fast, some huge and precise. The waiter calls Hugging Face again: "What files are in this repo?"

Hugging Face sends back the list — but only the ones ending in `.gguf`, and sorted smallest to largest. It also sends something invisible and critical: a revision hash. Think of it like a fingerprint. This is the exact version of the repo right now. If the repo changes tomorrow, this fingerprint will be different, and we'll know the model we downloaded is from a different moment in time.

You pick the file you want — say, the Q4_K_M quantization. The waiter now knows exactly what to get and exactly which version.

---

### 5. Naming the Model

Now the waiter needs to give this model a proper identity. From the repo name and the filename, it works out four things:

- The **base**: "mistral" — who made it.
- The **size**: "7b" — how many parameters.
- The **variant**: "instruct" — is it a chat model? A code model?
- The **quant**: "q4_k_m" — how compressed it is.

It stitches these together into a canonical name: `mistral:7b:instruct:q4_k_m`. This is the name the model will live under forever. The revision hash gets attached too — together, the name plus the hash is an ironclad promise that this exact model, in this exact version, is what you got.

---

### 6. Checking the Pantry, Revisited

Now that it has the full identity, the waiter checks the pantry one more time. `mistral:7b:instruct:q4_k_m` with this exact revision — do we already have it? The pantry is still empty. It's time to actually go get the model.

---

### 7. Asking the Neighbors

Before going all the way to Hugging Face's servers, the waiter tries something clever. Does anyone nearby already have this model?

It looks for a note on your computer. Is there a daemon running? The daemon is like a butler who lives in the background, handling all the seeding and sharing. If the daemon is home, the waiter knocks and asks: "Has anyone on the local network announced that they have this model?"

The daemon checks its memory. It's been listening to a quiet radio channel — a multicast address, `239.192.42.1:4269` — where any machine running hali on your LAN broadcasts what models it has. Every 30 seconds or so, each machine whispers into this channel: "I have mistral:7b:instruct:q4_k_m, here's my fingerprint." The daemon jots these down in a temporary notebook. It doesn't save them to disk — they're fleeting, like hearing someone's name at a party.

If a neighbor has the model, the daemon tells the waiter: "Yes, here's the infohash — the unique torrent fingerprint for that file." The waiter tells the daemon to start downloading.

Now two layers work together. The gossip channel answered *what* — it gave us the infohash. But we still need to know *where* — the IP address and port of the machine actually holding the pieces. That's the job of LSD, BitTorrent's built-in Local Service Discovery. The daemon broadcasts the infohash onto the LAN using LSD's multicast protocol, and Client A's daemon — which is already seeding — hears the query and responds with its address. The two daemons find each other, open a direct TCP connection, exchange the torrent info dictionary (piece hashes, file layout), and begin transferring data across the LAN. The waiter stands by, updating a progress bar every second, watching the bytes flow in. When it's done, it writes a receipt (metadata.json), announces success, and prints a magnet link — a shareable URI like `magnet:?xt=urn:btih:def789...` that anyone can paste into a BitTorrent client to join the swarm. No internet bandwidth spent. No Hugging Face involved.

But if nobody nearby has it — or if the daemon isn't even running — the waiter just shrugs and moves on. No harm done.

---

### 8. The Long Haul

No neighbor could help. Time to go to the source.

The waiter opens a direct line to Hugging Face's servers: `https://huggingface.co/mistralai/Mistral-7B-Instruct-v0.2-GGUF/resolve/main/that-file.gguf`. This is a plain HTTP download — no torrents, no peers. Just a simple stream of bytes from Hugging Face's CDN straight to a temporary file on your disk.

But there's an optional trick the waiter can pull. If you've set an environment flag, the waiter also pipes every byte through a piece hasher as it arrives — a tiny accountant that chops the incoming stream into 16-megabyte slices and computes the SHA-1 hash of each slice on the fly. By the time the file finishes downloading, all the piece hashes are already ready. The torrent identity — normally computed later in a separate pass — is known before the progress bar even reaches 100%. This saves a whole second read of the file later. (The default is to skip this, since most users just want the model and don't care about seeding speed.)

This could take a while. Models are gigabytes in size. So the waiter doesn't set a timer on the download — it could be minutes, it could be hours. Instead, it just keeps track of progress: every 150 milliseconds it checks how much has arrived and draws a progress bar on your screen. `[████████░░░░░░] 2.1 GB / 4.4 GB 47% 18.2 MB/s`.

If something goes wrong — the network drops, the server hangs up — the waiter cleans up. The half-finished `.tmp` file gets deleted so it doesn't clutter your disk.

If everything goes right, the temporary file gets renamed to its final name: `model.gguf`. Atomic. Instant. No half-files left behind.

At this point, the model file is on your disk, fully downloaded — but if streaming hash was off, it still has no torrent identity. Nobody knows its infohash. That comes later.

---

### 9. Writing the Receipt

The model file is on disk. But the pantry needs more than just the file — it needs to remember what this is and where it came from.

The waiter writes a file called `metadata.json` into the model's folder. It records:

- The model's canonical name (`mistral:7b:instruct:q4_k_m`)
- Where it came from (the Hugging Face repo)
- The revision fingerprint (so we know we got the right version)
- Which file was downloaded and how big it is
- The exact moment it finished downloading

The folder structure looks like a path you could guess: `~/.hali/models/mistral/7b-instruct/q4_k_m/model.gguf` and `metadata.json`. Everything organized by base, then size-variant, then quantization. You could navigate it yourself if you wanted to.

---

### 10. Waking the Butler

Now that the model is safely stored, the waiter has one more job: make it available to others.

It looks for the daemon again. Is it running? No? Then it launches one.

Starting the daemon is like hiring a butler on the spot. The waiter spawns a new background process — a completely separate running program that detaches from your terminal and goes to live quietly on its own. On Windows, it hides its window and starts a new process group. On Mac and Linux, it uses the classic Unix trick of creating a new session so it won't die when your terminal closes.

The new daemon wakes up, finds an empty port on your machine's loopback address (the one that only your own computer can reach), and writes its address to a file: `~/.hali/daemon.addr`. It also opens a second port for a web dashboard and writes that address too: `~/.hali/daemon.web`. Now anyone — the CLI, the web dashboard, future commands — can find the daemon just by reading these files.

The daemon immediately gets to work. It walks through `~/.hali/models/`, finds every model that's already downloaded, and starts seeding all of them. Each file gets its own torrent identity, and the daemon begins listening for peers who want pieces.

---

### 11. Sealing the Model for Sharing

Back in our story, the waiter now sends the daemon a message: "Seed this new model." It gives the daemon the file path, the model name, the HF repo, and the revision hash. If the streaming hash was enabled during download, it also sends a bag of precomputed piece hashes — meaning the daemon can skip right past the heavy work.

What happens next depends on whether those hashes arrived:

**Without streaming hash** (the default): The daemon takes the complete file — already sitting on disk from the HTTP download — and begins hashing it. This is the very first time the file is touched by the torrent system. It reads through the entire model file in 16-megabyte chunks, computing a unique SHA-1 fingerprint for each chunk. This is the slowest part of seeding, and it's a whole separate pass over the file. For a 4-gigabyte file, that's 256 chunks to hash. It could take a minute or two.

**With streaming hash**: The daemon already has the complete set of piece hashes. It doesn't need to re-read the file at all. It assembles the torrent info dictionary directly from the precomputed hashes — the same result as the full-file pass, but instantaneous. The model goes from "just downloaded" to "seeding" with no delay.

Either way, the infohash is born — a 20-byte torrent identity derived from all those piece hashes combined. The daemon creates what's called a torrent file — a small document that describes the model: its name, its size, how it's chopped into pieces, the hash of each piece, and some extra metadata. The comment field in this torrent file is a compact identity card:

> `{"model_id":"mistral:7b:instruct:q4_k_m","revision":"abc123...","format":"gguf","source":"huggingface"}`

No timestamps, no user info, no file paths from your machine. Just the model's public identity. This is what makes it possible for different people's torrents to be compatible — as long as they follow the same recipe, the hash will match, and their pieces will be interchangeable.

From that infohash, the daemon also forges a magnet link — a self-contained URI like `magnet:?xt=urn:btih:def789...&dn=mistral-7b-instruct-v0.2.Q4_K_M.gguf` that encodes the model's identity and any webseeds. Anyone who has this link can paste it into a BitTorrent client and join the swarm without needing a `.torrent` file at all.

The torrent file gets saved to `~/.hali/torrents/<infohash>.torrent`. The daemon loads it into its BitTorrent engine, verifies that all the pieces are present, and begins seeding. The magnet link appears in the web dashboard and in `hali daemon status` output. The model is now available to any peer on the network.

---

### 12. Broadcasting to the LAN

Now the daemon has a new model to announce. On its next broadcast cycle — somewhere between 25 and 40 seconds from now, with a random offset to avoid everyone shouting at once — it sends a message to the multicast address:

> *"I am node `a3f29b...`. I have `mistral:7b:instruct:q4_k_m`, infohash `def789...`, revision `abc123...`."*

But it doesn't just send those words in the clear. Before transmitting, the daemon signs the entire packet with HMAC-SHA256 using a shared secret stored in `~/.hali/lan.secret` — 32 random bytes generated automatically on first run. Any daemon that receives the packet re-derives the signature and checks it before trusting a single byte. Packets with a missing, wrong, or replayed signature are silently dropped. This means a rogue machine on the same network can't inject fake model announcements or redirect your downloads.

Any other machine on your LAN running bt with the same shared secret hears the announcement and verifies it. Their daemons jot it down in their in-memory notebooks: "That machine has Mistral 7B Instruct." Next time someone on that machine runs `hali pull mistral`, they might not need to download from Hugging Face at all — they'll get the file straight from you.

But the announcement doesn't last forever. After a couple of minutes, if the daemon hasn't heard from that node again, it lets the note fade. The LAN index is ephemeral — it's more like a rumor than a record.

---

### 13. The Aftermath

The waiter prints a final message: "Saved mistral:7b:instruct:q4_k_m (4.1 GB)." Then it disappears. Its job is done. The CLI process has no lingering state, no open connections, nothing left running. It was a transaction, not a session.

What remains:

- A model file on disk, organized neatly in `~/.hali/models/`
- A metadata receipt documenting exactly what was downloaded and when
- A daemon running in the background, seeding the model to LAN peers
- A torrent file on disk, ready to be reloaded instantly next time the daemon starts
- A magnet link — a compact, copy-pasteable URI that fully identifies the torrent — visible in the web dashboard and `hali daemon status`
- A quiet radio broadcast happening every 30 seconds, telling the neighborhood what's available

The next time you run `hali pull mistral`, that very first quick check will find the model already in the pantry. The waiter will say "Already downloaded" and exit in less than a second. No network calls. No hashing. Just a glance at a folder and a nod.

---

And that is the entire journey: from three words in a terminal, through a search across the internet, a simple HTTP download that might take hours, an optional parallel-hashing pass that eliminates the need to re-read the file, a torrent sealing that produces both a `.torrent` file and a magnet link, and finally a quiet broadcast that makes your machine a contributor to the neighborhood. Every step is independent — if Hugging Face is down, the LAN might save you. If the LAN is empty, Hugging Face will do. If the daemon crashes, the CLI just carries on. The system is designed like a federation of small, self-sufficient parts, each doing one thing well, and none of them depending on any other to survive.
