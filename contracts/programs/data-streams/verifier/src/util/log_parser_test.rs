use std::str::FromStr;

use super::LogParser;
use crate::events::AccessControllerSet;
use solana_program::pubkey::Pubkey;

#[test]
fn test_log_parser() {
    let program_logs = vec![
        "Program data: REMcjgMcJFMAAAAAAAAAAgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==".to_string(),
    ];

    let parsed_logs: Option<AccessControllerSet> = LogParser::parse_logs(program_logs);
    assert!(parsed_logs.is_some(), "Logs should be present");

    let logs = parsed_logs.unwrap();

    assert_eq!(logs.access_controller, Pubkey::from_str("1111111ogCyDbaRMvkdsHB3qfdyFYaG1WtRUAfdh").unwrap(), "Unexpected access controller");
}
