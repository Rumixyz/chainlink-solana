use snap::raw::{Decoder, Encoder};

pub struct Compressor {}

impl Compressor {
    pub fn compress(data: &[u8]) -> Vec<u8> {
        let mut encoder = Encoder::new();
        encoder.compress_vec(data).expect("Compression failed")
    }

    pub fn decompress(compressed: &[u8]) -> Vec<u8> {
        let mut decoder = Decoder::new();
        decoder.decompress_vec(compressed).expect("Decompression failed")
    }
}