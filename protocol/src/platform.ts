import { z } from 'zod';

export const messengerPlatformNames = ['buzz', 'signal', 'slack'] as const;

export const connectorPlatformNames = ['api', ...messengerPlatformNames] as const;

export const messengerPlatformSchema = z.enum(messengerPlatformNames);

export const connectorPlatformSchema = z.enum(connectorPlatformNames);

export type MessengerPlatform = z.infer<typeof messengerPlatformSchema>;

export type ConnectorPlatform = z.infer<typeof connectorPlatformSchema>;
