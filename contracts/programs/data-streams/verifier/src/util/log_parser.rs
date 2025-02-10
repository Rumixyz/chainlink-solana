use anchor_lang::{AnchorDeserialize, Discriminator};
use anchor_lang::__private::base64;

pub struct LogParser {}

impl LogParser {
    pub fn parse_logs<E: AnchorDeserialize + Discriminator>(
        logs: Vec<String>,
    ) -> Option<E> {

        let data_log = logs.iter().find(|log| log.starts_with("Program data: "))?;

        let data = data_log.strip_prefix("Program data: ")?;

        const DISCRIMINATOR_SIZE: usize = 8;

        let bytes = base64::decode(data)
            .ok()
            .filter(|bytes| bytes.len() >= DISCRIMINATOR_SIZE)?;

        let (discriminator, buffer) = bytes.split_at(DISCRIMINATOR_SIZE);

        E::discriminator()
            .eq(discriminator)
            .then(|| E::try_from_slice(buffer))?
            .ok()
    }
}