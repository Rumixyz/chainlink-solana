# Verifier Program

This program allows you to verify Chainlink Data Streams reports and retrieve verified reports.

## Features
Return Data Handling: Data by the program is set using the system call [set_return_data](https://docs.anza.xyz/proposals/return-data/).
So to retrieve the data from the program, the client should retrieve the return data from the transaction buffer.

## Usage
The below examples show how to interact with the verifier program and retrieve verified reports.

There is a lightweight Rust SDK for creating Solana program instructions to verify Chainlink Data Streams reports, supporting both on-chain and off-chain usage. 
- Link to the [Rust SDK](../../../crates/chainlink-solana-data-streams/README.md)

If you are using something other than rust you can use the Anchor IDL as a base to interface with the verifier program.
- Link to the [Anchor IDL](verifier-idl.json)

### Integration Examples
- [On-Chain Integration](https://docs.chain.link/data-streams/tutorials/streams-direct/solana-onchain-report-verification)
- [Off-Chain Integration](https://docs.chain.link/data-streams/tutorials/streams-direct/solana-offchain-report-verification)


## Developing

To generate the IDL - be in this program directory and run:
```bash
anchor build
```
The IDL will be in the `target/idl` directory.