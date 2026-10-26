# MNIST dataset

The loader uses the four gzip-compressed IDX files below. The SHA-256 values
are for the compressed file bytes and are checked before decompression.

| File | SHA-256 |
| --- | --- |
| `train-images-idx3-ubyte.gz` | `440fcabf73cc546fa21475e81ea370265605f56be210a4024d2ca8f203523609` |
| `train-labels-idx1-ubyte.gz` | `3552534a0a558bbed6aed32b30c495cca23d567ec52cac8be1a0730e8010255c` |
| `t10k-images-idx3-ubyte.gz` | `8d422c7b0a1c1c79245a5bcf07fe86e33eeafee792b84584aec276f5a2dbc4e6` |
| `t10k-labels-idx1-ubyte.gz` | `f7ae60f92e00ec6debd23a6088c31dbd2371eca3ffa0defaefb259924204aec6` |

## Source and licence

- Download source used for this checkout: `https://ossci-datasets.s3.amazonaws.com/mnist/`.
- The files were downloaded and their SHA-256 digests were computed locally
  before being recorded above. The original `yann.lecun.com` host was
  unreachable from this environment during this iteration.
- Licence/redistribution terms: **unverified**. No licence statement was
  available from the mirror response or verified from an accessible primary
  source during this iteration.

The data is intentionally not committed. Place all four verified files in
`data/`, or set `MNIST_DIR` to the directory containing them.

## Split and representation

`LoadData` checks each compressed file against the recorded digest, decompresses
gzip streams, validates the IDX magic numbers, counts, dimensions and payload
lengths, then normalizes each byte pixel to `float64(pixel)/255`. The first
50,000 canonical training examples form the training set and the remaining
10,000 form validation. The canonical 10,000-image test set is kept separate.
No random split is performed.
